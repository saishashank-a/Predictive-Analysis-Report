#!/usr/bin/env python

# Copyright 2016 VMware, Inc.
# All rights reserved. -- VMware Confidential

import os
import random
import sys
import time
import socket
import itertools
import json
import threading

from syslog import syslog, LOG_ERR, LOG_ALERT, LOG_WARNING

# __ssl_hack__
import ssl
if sys.version_info >= (2,7,9):
   if hasattr(ssl, '_create_unverified_context') and\
      hasattr(ssl, '_create_default_https_context'):
         ssl._create_default_https_context = ssl._create_unverified_context

try:
   import httplib
except ImportError:
   # __python3_hack__
   import http.client as httplib

def main(deployHostList, hostID):

   AUTODEPLOY_CONTACTED = False
   SLEEP_TIME = 10
   # Add sleep for retries with random linear backoff and maximum of 5 minutes.
   MAX_SLEEP = 5 * 60
   try:
      random.seed()
   except:
      syslog("failed to initialize random number generator.")

   syslog("notifying autodeploy at {}".format(deployHostList))

   try:
      # Try forever to signal that we are up.  We want to be resilient in case the
      # waiter or VC is down.
      deployHostIndex = 0
      while True:
         conn = None
         if SLEEP_TIME < MAX_SLEEP:
            try:
               SLEEP_TIME += random.randint(8, 12)
            except:
               syslog("failed to generate random number.")
               SLEEP_TIME += 10
         try:
            conn = httplib.HTTPSConnection(deployHostList[deployHostIndex],
                                           timeout=30)
            conn.request("POST", "/vmw/rbd/host/{}/up".format(hostID))
            response = conn.getresponse()
            if response.status == httplib.SERVICE_UNAVAILABLE:
               if AUTODEPLOY_CONTACTED:
                  syslog("autodeploy successfully notified -- "
                         "add-host in progress -- "
                         "retrying in %s seconds" % SLEEP_TIME)
               else:
                  AUTODEPLOY_CONTACTED = True
                  syslog("autodeploy successfully notified -- "
                         "add-host started -- "
                         "retrying in %s seconds" % SLEEP_TIME)
            else:
               syslog("autodeploy notify response -- %s %s" %
                      (response.status, response.reason))
            if response.status == httplib.OK:
               syslog("autodeploy successfully notified -- add-host finished.")
               break
            if response.status == httplib.NOT_FOUND:
               syslog("autodeploy does not know about this host")
               break
            if response.status == httplib.UNAUTHORIZED:
               syslog("autodeploy does not have valid credentials for this "\
                      "host.")
               break
            if response.status == httplib.CONFLICT:
               syslog("autodeploy could not add the host to vCenter.")
               break
            if response.status == httplib.GONE:
               syslog("autodeploy could not find the addhost/reconnect task.")
               break
         except socket.error as e:
            # Unable to reach autodeploy on this address, try another...
            syslog(LOG_ERR,
                   "could not connect to autodeploy at %s: %s" %
                   (deployHostList[deployHostIndex], e))
            deployHostIndex = (deployHostIndex + 1) % len(deployHostList)
            pass
         finally:
            if conn:
               conn.close()
         time.sleep(SLEEP_TIME)
   except Exception as e:
      syslog(LOG_ERR, "autodeploy notify error -- %s" % e)

if __name__ == "__main__":

   configFilePath = "/etc/vmware/autodeploy/waiterNotify.json"

   if os.path.isfile(configFilePath):
      with open(configFilePath, "r") as cfg:
         config = json.load(cfg)

      for option in ["deployHostList", "hostID"]:
         if not option in config:
            syslog(LOG_ALERT,
                   "Could not find '{}' in {}."\
                   .format(option, configFilePath))
            sys.exit(1)

      ret = os.fork()
      if ret == 0:
         main(config["deployHostList"],
              config["hostID"])
      elif ret < 0:
         syslog(LOG_ALERT, "Failed to fork()")


#!/usr/bin/env python

# Copyright 2016 VMware, Inc.
# All rights reserved. -- VMware Confidential

'''
This is the wrapper script which invokes all the files within the bundle.
The bundle needs to extracted out to /etc/rc.local.d/rbd directory.
Since rbd directory is not present this script does the job of fetching
the bundle and extracting out to the appropriate directory.
'''

import os
import sys
import time
import socket
import tarfile
import subprocess
import json

from syslog import syslog, LOG_ERR, LOG_ALERT, LOG_WARNING, LOG_INFO

# __ssl_hack__
import ssl
if sys.version_info >= (2,7,9):
   if hasattr(ssl, '_create_unverified_context') and\
      hasattr(ssl, '_create_default_https_context'):
      ssl._create_default_https_context = ssl._create_unverified_context

try:
    # __python3_hack__
    from io import BytesIO
except ImportError:
    # The python on ESXi prior to 6.0 did not have
    # io.BytesIO module included. So import StringIO.StringIO
    # as BytesIO.
    from StringIO import StringIO as BytesIO

try:
   import httplib
except ImportError:
   # __python3_hack__
   import http.client as httplib

def main(deployHost, bundleName, blackList, hostId):
   # The list of addresses that can be used to reach autodeploy.
   DEPLOY_HOSTS = deployHost
   SCRIPTS = []
   try:
       bundlePath = os.path.join("/etc/rc.local.d/autodeploy", bundleName)
       syslog(LOG_INFO, "Extracting scripts from %s" % bundlePath)
       with tarfile.open(name=bundlePath, mode= "r:gz") as tarHandle:
           for member in tarHandle.getmembers():
               if member.name not in blackList:
                   tarHandle.extract(member, "/etc/rc.local.d/autodeploy/scripts")
                   SCRIPTS.append(member.name)

       result = dict()
       if SCRIPTS:
           for script in SCRIPTS:
              try:
                 syslog(LOG_INFO, "Running %s script" % script)
                 proc = subprocess.Popen("/etc/rc.local.d/autodeploy/scripts/%s" % script,
                                         stdout=subprocess.PIPE,
                                         stderr=subprocess.PIPE)
                 out, err = proc.communicate()
                 rc = proc.returncode
                 if rc != 0:
                    result[script] = rc
              except Exception as err:
                 result[script] = str(err)
                 syslog(LOG_ERR,
                        "execute script %s failure: %s" % (script, str(err)))
           if result:
               # post the scripts status in json body.
               for deploy_host in DEPLOY_HOSTS:
                   try:
                       body = json.dumps(result)
                       conn = httplib.HTTPSConnection(deploy_host, timeout=30)
                       conn.request("POST", "/vmw/rbd/host/{}/script-status".format(hostId), body)
                       conn.getresponse()
                       conn.close()
                   except socket.error as e:
                       # Unable to reach autodeploy on this address, try another...
                       syslog(LOG_ERR,
                             "could not connect to autodeploy at %s: %s"
                              % (deploy_host, e))
   except Exception as e:
      syslog(LOG_ERR,
             "autodeploy scriptbundle process error -- %s" % e)

if __name__ == "__main__":

   # If secure boot is enabled we don't want to run 'arbitrary'/unsigned scripts.
   # Hence, we forbid downloading an archive of unknown scripts.
   secureBootCmd = "/usr/lib/vmware/secureboot/bin/secureBoot.py -s"

   try:
      secureBootEnabled = subprocess.check_output(secureBootCmd.split())
   except Exception as e:
      syslog(LOG_ERR, "Querying secure boot failed")
      sys.exit(1)

   configFilePath = "/etc/vmware/autodeploy/scriptBundle.json"
   config = None
   if os.path.isfile(configFilePath):
      with open(configFilePath, "r") as cfg:
         config = json.load(cfg)

      for option in ["deployHost", "bundleName", "blackList", "hostId"]:
         if not option in config:
            syslog(LOG_ALERT,
                   "Could not find {} in {}."\
                   .format(option, configFilePath))
            sys.exit(1)


   if "Enabled".encode() in secureBootEnabled:
      syslog(LOG_WARNING,
             "This script is not allowed to run with secure boot enabled.")
      if config:
          bundlePath = os.path.join("/etc/rc.local.d/autodeploy",
                                     config["bundleName"])
          syslog(LOG_INFO, "Secure boot enabled, removing path: %s" %
                  bundlePath)
          try:
              os.unlink(bundlePath)
          except OSError as e:
              syslog(LOG_ERR, "Failed to remove the %s: %s" %
                      (bundlePath, e))
      sys.exit(0)

   if config:
       main(config["deployHost"],
            config["bundleName"],
            config["blackList"],
            config["hostId"])

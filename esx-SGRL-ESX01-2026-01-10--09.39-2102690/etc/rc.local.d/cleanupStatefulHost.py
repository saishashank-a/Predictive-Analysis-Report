#!/usr/bin/python
########################################################################
# Copyright 2017 VMware, Inc.  All rights reserved.
# -- VMware Confidential
########################################################################
"""
   This script checks if the host is stateful installed host and cleanups
   the boot.cfg to remove any stateless boot configurations.
"""
import os
import sys
import vmkctl
from syslog import syslog, LOG_DEBUG, LOG_ERR

MODULE_NAME = "waiter.tgz"
BOOT_CFG = "/bootbank/boot.cfg"
BOOTBANK_WAITER = "/bootbank/waiter.tgz"
TARDISK_WAITER = "/tardisks/waiter.tgz"

def cleanupBootConfig():
   """
   The host booted from autodeploy has waiter.tgz as part of the list
   of modules that ESX boots with. Once this stateless host does stateful
   install to USB or local disk and boots from it, this waiter.tgz module
   needs to be removed. This function removes the waiter.tgz module from
   boot.cfg.
   """

   syslog(LOG_DEBUG,
          "Host stateful installed by autodeploy, running cleanup check")
   cleanup = False
   with open(BOOT_CFG, 'r') as fp:
      lines = []
      for line in fp.readlines():
         # Check if MODULE is present in boot.cfg.
         if line.startswith('modules=') and line.find(MODULE_NAME) != -1:
            line = line.replace(' --- %s' % MODULE_NAME, '')
            syslog("Removing %s from boot.cfg" % MODULE_NAME)
            cleanup = True
         lines.append(line)

   if cleanup:
      with open(BOOT_CFG, 'w') as fp:
         fp.writelines(lines)
      syslog("Boot.cfg cleaned up after stateful install.")
   if os.path.exists(BOOTBANK_WAITER):
      os.unlink(BOOTBANK_WAITER)

if __name__ == "__main__":
   try:
      # Check if the host is stateful installed host and waiter.tgz is loaded
      # in memory.
      info = vmkctl.SystemInfoImpl()
      if os.path.exists(TARDISK_WAITER) and info.IsStatefulInstallBooted():
         cleanupBootConfig()
      sys.exit(0)
   except Exception as e:
      syslog(LOG_ERR, "Script failed to run. %s" % e)
      sys.exit(1)

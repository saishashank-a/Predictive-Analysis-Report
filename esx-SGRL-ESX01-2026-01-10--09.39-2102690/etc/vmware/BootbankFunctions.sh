#!/bin/sh
# Copyright 2009 VMware Inc.
#
# Two exported functions are provided, one verbose and implementing a policy
# of unconditional exit on failure, the other silent and returning failures
# via return status.  These conform respectively to the policies needed by
# upgrade and backup.  Callers should choose according to their requirements,
#

BbLog()
{
   echo "$(date "+%Y-%m-%d %H:%M:%S") $*"
}

BbPanic()
{
   echo "$(date "+%Y-%m-%d %H:%M:%S") operation aborted Error: $*"

   echo "Error: $*" >&2

   esxcfg-init --alert "$*"

   exit 1
}

#
# Verify using gzip, tar, vmtar, including "wrapped" tar archives.
# Failures are silent.  Caller must check $? for a non-zero return status.
#
VerifyCopiedArchive()
{
   local archive="$1"
   local isWrapped=${2:-0}
   local res=0

   if [ ! -f "${archive}" ] ; then
      return 1
   fi
   
   # Check if the archive is one of the b.z (previously vmkboot.gz)
   # k.z (previously vmk.gz)
   # s.z (previously sys.vgz)
   # m.z (previously mod.tgz)
   # If yes, then we need to handle them specially here as the 
   # extensions won't tell what kind of archige it is anymore.

   local file=$(basename "${archive}")
   case "${file}" in
      *.tgz)
         if [ "$isWrapped" = "0" ]; then
            tar tzvf "${archive}" 2>&1 > /dev/null
            res=$?
         else
            tar xzOf "${archive}" | tar tzv 2>&1 > /dev/null
            res=$?  
         fi          
         ;;
      *.gz | b.z | k.z)
         gunzip -c "${archive}" 2>&1 > /dev/null
         res=$?
         ;;
      m.z)
         tar tzvf "${archive}" 2>&1 > /dev/null
         res=$?
         ;;
      s.z)
         vmtar -t < "${archive}" 2>&1 > /dev/null
         res=$?
         ;;
   esac

   return $res
}

#
# Copy and verify using md5sum and optionally with VerifyCopiedArchive()
# Operation is verbose; failures result in a sysAlert and an unconditional exit
#
CopyAndVerify()
{
   local src=$1
   local dest=$2
   local isWrapped=${3:-0}
   local dgst_1=
   local dgst_2=

   BbLog "Copying ${src} to ${dest}..."
   # if the source file is not there, no copy is possible
   if [ ! -f "$src" ]; then
      BbPanic "File to copy ${src} does not exist!"
   fi

   # try to copy
   cp "$src" "$dest"

   # verify the copy
   cmdline="md5sum"
   dgst_1=$(${cmdline} "${src}" 2> /dev/null | cut -d" " -f1)
   dgst_2=$(${cmdline} "${dest}" 2> /dev/null | cut -d" " -f1)

   # if any of the files don't get a md5 sum string, something is
   # wrong
   if [ -z "$dgst_1" ]; then
      BbPanic "Can't obtain digest for ${src}"
   fi

   if [ -z "$dgst_2" ]; then
      BbPanic "Can't obtain digest for ${dest}"
   fi

   # if they don't match the copy failed.
   if [ "${dgst_1}" != "${dgst_2}" ]; then
      BbPanic "Copy of ${src} to ${dest} failed md5sum validation"
   else
      BbLog "Verifying Archive: ${Verifying}"
      VerifyCopiedArchive "${dest}" "${isWrapped}" 
      if [ "$?" != "0" ]; then
         BbPanic "${dest} failed unzip and/or archive validation"
      fi
   fi
}

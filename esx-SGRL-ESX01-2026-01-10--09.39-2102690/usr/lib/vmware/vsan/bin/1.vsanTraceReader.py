#!/usr/bin/python

###############################################################################
#
# Initializes a trace reader. The traces are read off a set of input files.
# Traces are expected to be in binary
# format and are subsequently either written to the disk in the specified output
# file.
#
# Other options that can be implemented for writing out the entries are:
# - Cache in memory.
# - write to a distributed database?
#
###############################################################################

from optparse import OptionParser
import sys
import errno
import os

def AugmentLibrarySearchPaths():
   """ Augments sys.path with a few educated guesses. """

   guesses = []
   ver = sys.version_info
   relpath = 'lib/python%u.%u/site-packages' % (ver[0], ver[1])

   # Perhaps we're running the trace reader out of a support bundle.
   base = os.path.dirname(os.path.abspath(__file__))
   while base != '/':
      guesses.append(os.path.join(base, relpath))
      base = os.path.dirname(base)

   # Otherwise, fall back on $VMTREE
   vmtree = os.environ.get('VMTREE', None)
   vmbld = os.environ.get('VMBLD', 'obj')
   if vmtree:
      base = os.path.join(vmtree, 'build/esx', vmbld, 'vmvisor/sys')
      guesses.append(os.path.join(base, relpath))

   for g in guesses:
      if g not in sys.path:
         sys.path.append(g)

try:
   from vmware.vsan.traceEntry import TraceLogEntry
except ImportError:
   AugmentLibrarySearchPaths()
   try:
      from vmware.vsan.traceEntry import TraceLogEntry
   except ImportError:
      print('Failed to find VSAN python modules in the usual places:')
      for p in sys.path:
         print(' - ' + p)
      raise

#
# Basic reader class, everyone overrides this.
#
class VsanTraceReader():
    def __init__(self, name):
        self.name = name

    def readEntry(self):
        lastIdx = None

        while True:
            try:
                e = self.readOneEntry()
            except EOFError:
                break

            if lastIdx != None and lastIdx < e['idx']:
                delta = e['idx'] - lastIdx
                if delta != 1:
                    sys.stderr.write("Detected gap: %u -> %u (%u)\n" %
                                     (lastIdx, e['idx'], delta))
            lastIdx = e['idx']

            yield e

    def readEntryWithTS(self):
        foundWallClock = False
        queue = []

        # Queue entries until we find a wallclock reference.
        for e in self.readEntry():
            # Don't yield the wallclock entry itself.
            if foundWallClock and e['name'] != 'TracingTraceWallClock':
                yield e

            elif e['name'] != 'TracingTraceWallClock':
                queue.append(e)

            else:
                # Interpret the entry, thereby updating the time reference.
                e.RequireTypedSlotValues()

                # Explicitly update timestamp for deferred entries.
                queue.append(e)
                for e in queue:
                   e.CalcAbsTimestamp()
                   yield e

                foundWallClock = True
                queue = []

        if not foundWallClock:
           sys.stderr.write("No wallclock entry detected. There might be a mismatch between "
                            "the reader version and the tracefile.\n")
           for e in queue:
               yield e

    def __close__(self):
        raise NotImplementedError

    def __str__(self):
        return self.name

    def openfile(self):
       raise NotImplementedError

    def getcurrentfile(self):
       raise NotImplementedError

    def closefile(self):
       raise NotImplementedError

    def nextFile(self):
       raise NotImplementedError

#
# Sets up a reader for vsan trace entry from stdin
#
class VsanTraceReaderStdin(VsanTraceReader):
   
   def __init__(self, name, srcfiles):
      VsanTraceReader.__init__(self, name)
      self.readerfp = sys.stdin

   def readOneEntry(self):
      return TraceLogEntry(self.readerfp)

   def closefile(self):
      pass

#
# Sets up a reader for vsan trace entry from a list of input files
#
class VsanTraceReaderFile(VsanTraceReader):
    #
    # srcfiles: input files to read the traces from: expected binary input.
    # readerfp: current file reader descriptor.
    # srcfileiter: iteator to go over the input files.
    #
    # Exception should be handled by callers.
    #
    srcfiles = None
    readerfp = None
    srcfileiter = None    
    def __init__(self, name, srcfiles):
        VsanTraceReader.__init__(self, name)
        self.srcfiles = srcfiles
        self.srcfileiter = iter(self.srcfiles)
        self.currentfile = None
        self.openfile()
        assert (self.currentfile != None)

    #
    # Opens the file corresponding to where the iterator is at this time.
    # Closes any already opened file, if any. Exceptions should be handled
    # from the callers.
    #
    def openfile(self):
        filename = next(self.srcfileiter)

        if self.readerfp != None:
            self.closefile()

        if filename[-3:] == ".gz":
           import gzip
           self.readerfp = gzip.open(filename, 'rb')
        else:
           self.readerfp = open(filename, 'rb')

        self.currentfile = filename

    #
    # Get the filename that's currently read from.
    #
    def getcurrentfile(self):
       return self.currentfile
    
    #
    # Moves the cursor to the next file in the list of input files.
    # if no more input files are there, None is returned, otherwise True is
    # returned.
    #
    # Exceptions should be handled by callers.
    #
    def nextfile(self):
        try:
            self.closefile()
            self.openfile()
            return True
        except StopIteration:
            return None

    #
    # Close the file that's pointed by the current reader descriptor.
    #
    def closefile(self):
        if self.readerfp != None:
            self.readerfp.close()
    def __close__(self):
        self.closefile()

    #
    # Read a VsanTraceEntry from the current offset in the current file descriptor
    # that is opened. If no entry can be read then next file in the input list
    # is tried. This is a generator funciton and returns one entry at a time.
    #
    def readOneEntry(self):
        while True:
            try:
                return TraceLogEntry(self.readerfp)

            except EOFError:
                if self.nextfile() != None:
                    continue
                else:
                    raise

            except IOError as e:
                if str(e).startswith('CRC check failed'):
                    raise EOFError

#
# An abstract trace writer class.
#
class VsanTraceWriter():

    def __init__(self, name, tofilter):
        self.name = name

        self.nofilter = True
        self.filters = []
        if tofilter != None:
           self.filters = tofilter.split(',')
           self.nofilter = False

        assert (self.nofilter or len(self.filters) > 0)

    def getname(self):
        return self.name

    #
    # Match the entry with the installed filters. Note that
    # if no filters are installed everything will match.
    #
    # return:
    # <True, entrystr> if tostr is True and match is found
    # <True, None> if tostr is False and match is found
    # <False, None> if match isn't found.
    #
    def matchEntry(self, entry, tostr=False):
       if self.nofilter:
          if not tostr:
             return True, None
          else:
             return True, str(entry)

       estr = str(entry)
       for f in self.filters:
          if estr.find(f) != -1:
             return True, (estr if tostr else None)

       return False, None
    
    #
    # Check if there are any filters installed for the writer
    #
    def filterEnabled(self):
       return not self.nofilter

    #
    # Read traces from input set of input files and write it back to destination.
    #
    def writer(self, trf):
        raise NotImplementedError
    #
    # Closes the writer.
    #
    def closewriter(self):
        raise NotImplementedError

#
# This can be very inefficient if the number of entries are too large.
# use with caution.
#
class VsanTraceWriterCache(VsanTraceWriter):
    def __init__(self, name, tofilter):
        VsanTraceWriter.__init__(self, name, tofilter)
        self.entries = []

    #
    # Read traces from input set of input files and cache them into the list.
    #
    def writer(self, trf):
        for e in trf.readEntryWithTS():
            match, estr = self.matchEntry(e)
            if match:
                self.entries.append(e)

#
# Writer which writes to a given file or stdout.
#
class VsanTraceWriterFile(VsanTraceWriter):
    writerfp = None
    def __init__(self, name, tofilter):
        VsanTraceWriter.__init__(self, name, tofilter)
        self.writerfp = sys.stdout

    #
    # Read traces from specified tracereader instance (trf) and write to given
    # file descriptor. 
    #
    def writer(self, trf):
        for e in trf.readEntryWithTS():
            match, estr = self.matchEntry(e, True)
            if match:
                currentfile = \
                    (trf.getcurrentfile() + ':') if self.filterEnabled() else ''
                self.writerfp.write(currentfile + estr + '\n')

    #
    # Close the writer.
    #
    def closewriter(self):
        if self.writerfp != sys.stdout:
            self.writerfp.close()
            
#
# Get input files. input files can be specified in various ways as
# specified in the help.
#
def getInputFiles(arg):
   import os

   if len(arg) == 1 and os.path.isdir(arg[0]):
      import glob
      inputfiles = glob.glob(os.path.join(arg[0], 'vsantraces--*.gz'))
      return inputfiles

   else:
      return arg

#
# Parse arguments, and setup the appropriate reader/writer.
#
def main():
    inputfilesHelp = '''\nEverything on the command line after options is considered to
be input files. If nothing is specified than the input is assumed to be taken from the
stdin. If input files are specified they can be specified in one of the following ways:
1. Any # of absolute filenames, which will be read in the order they are specified.
2. filename with wild card, ex: "/scratch/log/vsantraces--*.gz" . Files will be read
in the order that they appear in the directory listing. NOTE: that you need to specify
the filename within the quotes.
3. directory name. This is equivalent to specifying <dir>/vsantraces--*.gz'''

    usageStr = 'Usage: %s [options] [input-files] \n%s' % (sys.argv[0], inputfilesHelp)
    parser = OptionParser(usageStr)
    
    filterHelp = '''Specify a set of filters to look for when traces are read off. 
With this option, the file name will also be printed along with the trace that matched.
This is just like doing a grep but done internally. You can specify multiple 
comma-separated filters. If a filter has space in it use double quotes to surround that
filter.'''
    parser.add_option('-f', '--filter', dest='filter', default=None, help=filterHelp)

    opts, args = parser.parse_args()

    inputfiles = getInputFiles(args)

    w = VsanTraceWriterFile('trace-writer-stdout', opts.filter)
    
    trf = None
    
    if (len(inputfiles) == 0):
       trf = VsanTraceReaderStdin('vsantrace-stdin-reader', None)
    else:
       trf = VsanTraceReaderFile('vsantrace-reader', inputfiles)
    
    w.writer(trf)
        
if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        pass
    except IOError as e:
        if e.errno == errno.EPIPE:
           pass
        else:
           raise

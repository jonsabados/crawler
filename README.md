# crawler

A simple web crawler limited to a single domain.

## Build Requirements

Building crawler requires go 1.13<sup>1</sup> to be on the path as well as make. Go is available here along with 
installation instructions: https://golang.org/doc/install#install.

If you are running OS X you can install make either through homebrew or through xcode. For homebrew see 
https://brew.sh/ for installing brew, then just run `brew install make`. Alternatively installing `make` and other
command line tools with xcode you may follow the instructions on this page: http://osxdaily.com/2014/02/12/install-command-line-tools-mac-os-x/

## Building

Once the required tools have been installed simply run `make` from within a directory this codebase has been cloned into.

<sup>1: It will likely build fine for any version of go supporting go mod (1.11+) but has only been tested with go 1.13</sup>

## Tests

To execute unit tests one may run `make test`

## Running

A binary, `crawl` will be produced in the `dist` directory after running make. The usage of it is:

```
$ ./dist/crawl
Usage of ./dist/crawl:
  -executionTimeout int
    	total execution timout, in seconds (default 120)
  -readTimeout int
    	http read timeout in milliseconds (per URL seen) (default 500)
  -url string
    	required - starting point for crawl and must be an http or https url. Only links on the same domain will be searched
  -workers int
    	how many workers to execute with (default 10)
```

the only required argument is -url. -readTimeout is likely to short for some sites, this is the exact usage I settled
on when doing my real-world test:

`./dist/crawl -url  http://wiprodigital.com -readTimeout 10000 -workers 100`

## Solution reasoning

Choosing go as a language:
* Its a language I've been working with recently and like to think I'm fairly strong in it
* There aren't a ton of required tools to get it building so its fairly easy to be confident my build instructions will
work without having to jump through a bunch of hoops. Its also really easy to do cross platform compilation and the
binaries produced wouldn't have any need for shared libraries so it would be easy to produce executables for others to
run
* Go makes doing concurrent work a little bit easier than many other languages.

Concurrency:
In some ways this was a pretty straight forward task - reading urls and extracting all the links in them was a no
brainer. Crawling things however offered the opportunity for some concurrency since it was going to involve a whole
slew of network requests and doing things in serial could result in very large run-times. Normally worker pools in go
are pretty straight forward: fire up a channel to feed work into, create a wait group to signal worker completion,
and then fire up your worker routines that call .Done() on the wait group when complete and then just wait for those to
finish - ideally they can just dump work output into another channel that has a listener on it building up results.

This quickly blew up though, since the workers were also the work producers. The initial version of this relied on 
waiting for all workers in a worker pool to be idle, which just felt wrong. This version does a more standard recursive
crawl function, but with subsequent calls in separate go routines allowing for straight forward use of wait groups
to wait for nested call completion (this would also allow for depth tracking). This also allows for a more straight 
forward worker pool to execute those requests (this allows for control over the # of concurrent requests being made)

## areas for improvement

 * This just finds all links, it would be cool to making use of link text (also requires knowing what to do if the link 
 uses an image and so on) so I put this in the "out of scope" category
 * doing more than just logging errors when parsing html - there's a number of things that could be done, probably
 involving passing callbacks or something but there would need to be an actual requirement of desired behavior to do
 something more than theoretical so also out of scope.
 * looking at content types of links - right now it assumes that it'll get text content from every link and doesn't
 bother to check the content type before processing it. If someone did do a link to something that caused the html parser
 to barf it'll just keep on trucking but seems like the type of thing that would be include in enhanced error reporting
 * log level could be made into an argument or read from an environmental variable
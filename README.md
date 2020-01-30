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

## 

## areas for improvement

 * making use of link text (also requires knowing what to do if the link uses an image and so on)
 * doing more than just logging errors when parsing html
 * looking at content types of links
 * idle worker pool monitoring stuff could probably be extracted
 * log level could be made into an argument
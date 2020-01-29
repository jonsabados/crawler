DEFAULT_GOAL: dist/crawl

dist/:
	mkdir dist

dist/crawl: dist/ $(shell find . -name '*.go')
	go build -o dist/crawl

.PHONY: test
test:
	go test ./... --race --cover

.PHONY: clean
clean:
	rm -rf dist/
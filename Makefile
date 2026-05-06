.PHONY: build clean

build:
	go build -o bin/bori-devspace ./adapters/devspace

clean:
	rm -rf bin/

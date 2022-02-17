all: build/route-table

rebuild: clean build/route-table

clean:
	rm -rf build/*

build/route-table: lint
	CGO_ENABLED=0 go build -o build/route-table

lint:
	golint -set_exit_status ./...

.PHONY: all clean lint
`promrunner` is a tool that runs the Prometheus binary from a GitHub pull request.

Technically it will download the binary from the CircleCI 'test' job associated to the pull request and exec it.

## Build

The project uses [go modules](https://github.com/golang/go/wiki/Modules) so it requires go with support for modules.

```
go build ./cmd/promrunner/...
```

## Usage

```
./promrunner -h  // Usage and examples.
```


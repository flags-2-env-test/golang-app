FROM golang:1.23-bookworm

WORKDIR /app

RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential make \
 && rm -rf /var/lib/apt/lists/*

COPY .vendor/.zed/oresoftware/flags-2-env ./.vendor/.zed/oresoftware/flags-2-env

COPY .cli-flags.toml ./
COPY go.mod ./
COPY src ./src

ENV GOCACHE=/tmp/go-build
ENV GOPATH=/tmp/go
ENV CGO_ENABLED=1

# Go statically compiles parser.c through cgo, so there is no shared library and
# nothing to resolve at runtime. The go.mod replace directive points the module
# path at [install].dir.
RUN go build -o /tmp/demo ./src

CMD ["/tmp/demo"]

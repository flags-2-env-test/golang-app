FROM golang:1.23-bookworm@sha256:167053a2bb901972bf2c1611f8f52c44d5fe7e762e5cab213708d82c421614db

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
# path at [install].dir, so the module graph never reaches the proxy -- hence
# -mod=mod with no go.sum rather than a lockfile.
RUN go build -o /tmp/demo ./src

RUN useradd --create-home --shell /bin/sh --uid 10001 fixture
USER fixture

CMD ["/tmp/demo"]

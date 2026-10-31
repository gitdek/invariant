# The plumbing sandbox (D-0108): the Python image the gate runs Python in,
# pinned by digest, so a build tests collector code under the interpreter it
# runs under in production (#332). It has the Go toolchain from the Go image
# the gate uses, and git, since the repository's own tests build git
# repositories. INVARIANT_PYTHON_PINNED tells the tests that python3 is the
# image's. It runs with no network; only building it needs one.
FROM golang@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS go
FROM python@sha256:79e7a9b9ff1cbceff819f856fb374477792a5967759d94df266de7b7b4120e6f
COPY --from=go /usr/local/go /usr/local/go
ENV GOTOOLCHAIN=local GOPATH=/go PATH=/go/bin:/usr/local/go/bin:$PATH INVARIANT_PYTHON_PINNED=1
RUN apk add --no-cache git=2.54.0-r0 && mkdir -p /go/src /go/bin && chmod -R 1777 /go

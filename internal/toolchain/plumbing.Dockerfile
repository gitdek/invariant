# The plumbing sandbox (D-0108): the Go image the gate uses, pinned by
# digest, with git added, since the repository's own tests build git
# repositories, and Python 3, since discovering Python components parses
# their source with Python's own standard library. It runs with no
# network; only building it needs one.
FROM golang@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414
RUN apk add --no-cache git=2.54.0-r0 python3=3.14.10-r1

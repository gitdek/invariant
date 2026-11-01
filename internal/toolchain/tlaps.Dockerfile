# The TLAPS sandbox (#360): the TLA+ Proof System's tlapm, with the backends
# its release bundles: Z3 for SMT, Zenon, Isabelle/TLA+ and LS4 for
# propositional temporal logic. It's built for linux/amd64 only, from the
# official x86_64 Linux build, so a laptop and CI run the same binaries.
#
# Every input is pinned. The base is the Temurin image TLC runs in
# (toolchain.JavaImage), pinned by digest, for the glibc tlapm's binaries
# need. The tarball is pinned by its SHA-256. Its release, 1.6.0-pre, is
# rolling and replaces its asset, so this fetches an unmodified copy of the
# validated tarball from a release of Invariant's own (#387). If those bytes
# ever change, this build fails instead of installing another tlapm, and
# moving the pin is a decision.
#
# The tarball is unpacked on the building machine's own platform, since tar
# can't create directories under some amd64 emulators, and only its files
# are copied into the linux/amd64 image.
FROM --platform=$BUILDPLATFORM eclipse-temurin@sha256:49e21e16e3c86eb7816a44a67549910ed090fbeb40c29c525d58bf5e02e91b0f AS unpack
ADD --checksum=sha256:b0038e59a6bddc9aa9a75e7c3320e20333f02fd7ba745ea64f1cc1c9800e43e8 https://github.com/gitdek/invariant/releases/download/tlapm-1.6.0-pre-eef4b65/tlapm-1.6.0-pre-x86_64-linux-gnu.tar.gz /tmp/tlapm.tar.gz
RUN tar -xzf /tmp/tlapm.tar.gz -C /opt

FROM --platform=linux/amd64 eclipse-temurin@sha256:49e21e16e3c86eb7816a44a67549910ed090fbeb40c29c525d58bf5e02e91b0f
COPY --from=unpack /opt/tlapm /opt/tlapm
RUN /opt/tlapm/bin/tlapm --version
ENV PATH=/opt/tlapm/bin:$PATH
WORKDIR /work
ENTRYPOINT ["tlapm"]

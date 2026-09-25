# The Nagini sandbox (D-0031): Python 3.12, a Java runtime for the Viper
# verifier, and Nagini. It's built for linux/amd64 only. Nagini pins a
# different Z3 on ARM, and one image everywhere keeps receipts identical
# between a laptop and CI.
#
# The base is pinned by digest, and every Python package by the hash of the
# exact wheel, so the recipe's hash names what gets installed. The Java
# runtime comes from Debian and isn't pinned yet.
FROM --platform=linux/amd64 python@sha256:2f17fc044b579bab302c2e8054d3a686e2cb9a83de48e70534b94cd8ebbe06a9
RUN apt-get update \
 && apt-get install -y --no-install-recommends openjdk-21-jre-headless \
 && rm -rf /var/lib/apt/lists/*
ENV JAVA_HOME=/usr/lib/jvm/java-21-openjdk-amd64
RUN pip install --no-cache-dir --no-deps --require-hashes -r /dev/stdin <<EOF
nagini==1.3.1 --hash=sha256:56d85c9a55baa3a769d849138ff2e49c0a1627424e680aea96f76fce96c4206c
jpype1==1.7.1 --hash=sha256:4de86ec7f9f381c7aea8cbbecaa189c020e5fb700620bd96f4762f954757656b
mypy==1.5.0 --hash=sha256:69b32d0dedd211b80f1b7435644e1ef83033a2af2ac65adcdc87c38db68a86be
mypy_extensions==1.1.0 --hash=sha256:1be4cccdb0f2482337c4743e60421de3a356cd97508abadd57d47403e94f5505
typing_extensions==4.16.0 --hash=sha256:481caa481374e813c1b176ada14e97f1f67a4539ce9cfeb3f350d78d6370c2e8
packaging==26.3 --hash=sha256:d7193f7c8e4e93f444fde0262bf90af30e16fa0ad0ad44cb553c87339b23cd1c
toposort==1.10 --hash=sha256:cbdbc0d0bee4d2695ab2ceec97fe0679e9c10eab4b2a87a9372b929e70563a87
z3-solver==4.8.7.0 --hash=sha256:ecdf7cb3df9661bdc8953a552a7c09e1c0b7f4195e5eeeab8ff90f672490bf3f
EOF
RUN pip check
WORKDIR /work
ENTRYPOINT ["nagini"]

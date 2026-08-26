# SPDX-License-Identifier: GPL-3.0-only
FROM fedora:latest

RUN dnf update -y && \
    dnf install -y \
        coreos-installer \
        butane \
        git \
        ignition-validate \
        python3 && \
    dnf clean all

WORKDIR /output
WORKDIR /opt

COPY create-iso.py .
COPY server.bu .
COPY scripts ./scripts

ENTRYPOINT ["python3", "./create-iso.py"]

# SPDX-License-Identifier: GPL-3.0-only
FROM fedora:latest

RUN dnf update -y && \
    dnf install -y \
        coreos-installer \
        butane \
        git \
        ignition-validate \
        python3 \
        python3-pip \
        python3-passlib \
        python3-jinja2 && \
    dnf clean all

WORKDIR /output
WORKDIR /opt

COPY create-iso.py .
COPY server.bu.j2 .
COPY manifests ./manifests

ENTRYPOINT ["python3", "./create-iso.py"]

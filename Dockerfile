FROM fedora:latest

RUN dnf update -y && \
    dnf install -y \
        coreos-installer \
        butane \
        ignition-validate \
        python3 \
        python3-pip \
        python3-jinja2 && \
    dnf clean all

WORKDIR /output
WORKDIR /opt

COPY create-iso.py .
COPY server.bu.j2 .

ENTRYPOINT ["python3", "./create-iso.py"]

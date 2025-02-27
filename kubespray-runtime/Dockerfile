FROM docker.io/mgoltzsche/podman:minimal
RUN apk update
RUN apk add --no-cache python3 py3-pip openssh-client sshpass rsync
COPY *.txt /root/
RUN pip3 install --break-system-packages -r /root/requirements-kubespray.txt
RUN pip3 install --break-system-packages -r /root/requirements-inventory_builder.txt

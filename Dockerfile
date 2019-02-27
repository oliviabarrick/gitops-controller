FROM alpine

ENV SSH_KNOWN_HOSTS=/ssh/known_hosts

RUN apk update && apk add openssh
RUN mkdir /ssh && \
    ssh-keyscan github.com > /ssh/known_hosts && \
    ssh-keyscan gitlab.com >> /ssh/known_hosts

WORKDIR /

COPY ./run.sh /run.sh
COPY ./git-controller /git-controller

ENTRYPOINT ["/run.sh"]

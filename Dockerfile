FROM scratch

COPY ./git-controller /git-controller

ENTRYPOINT ["/git-controller"]

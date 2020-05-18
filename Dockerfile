FROM alpine:3

ENTRYPOINT ["/usr/local/bin/docre"]

COPY dist/linux/amd64/docre /usr/local/bin/

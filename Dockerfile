FROM alpine:3

ENTRYPOINT ["/usr/local/bin/docre"]

COPY docre /usr/local/bin/

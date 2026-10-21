FROM gcr.io/distroless/static-debian12:nonroot
COPY piplon /piplon
COPY scenarios /scenarios
USER nonroot:nonroot
ENTRYPOINT ["/piplon"]
CMD ["run", "--config", "/etc/piplon/piplon.yaml"]

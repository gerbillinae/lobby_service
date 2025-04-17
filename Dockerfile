FROM alpine:latest

WORKDIR /root/

COPY ./* .

EXPOSE ${APP_PORT}

CMD ["./main"]


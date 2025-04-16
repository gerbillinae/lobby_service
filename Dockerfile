FROM alpine:latest

WORKDIR /root/

COPY ./main .
COPY ./package.env .

EXPOSE 8080

CMD ["./main"]


FROM golang

RUN go get github.com/Masterminds/glide
RUN go get github.com/vinler/hydra-consent-app-go

WORKDIR $GOPATH/src/github.com/vinler/hydra-consent-app-go

RUN glide install

ENTRYPOINT go run main.go

EXPOSE 4445


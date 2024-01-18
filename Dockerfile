FROM ubuntu:22.04

WORKDIR /client/

RUN apt-get update && \
	apt-get upgrade -y 

RUN apt-get install -y torbrowser-launcher

RUN apt-get install -y golang-go

COPY ./ /client/DPT
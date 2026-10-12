FROM mcr.microsoft.com/devcontainers/ruby:3.2-bookworm AS base

RUN apt-get update && apt-get install -y vim

FROM base

RUN mkdir -p /app
WORKDIR /app
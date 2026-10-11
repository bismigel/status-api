# APPNAME — container image.
# REPLACE the placeholder in app/ with your application and adjust this file.
#
# This default serves the static placeholder in app/ with nginx on port 80.
# For a real app: switch the base image (e.g. node:22-alpine, golang, python)
# and add your build/run steps. Make sure the app listens on $APP_PORT.
FROM docker.io/library/nginx:alpine

COPY app/ /usr/share/nginx/html/
# COPY deploy/nginx.conf /etc/nginx/nginx.conf   # optional custom config

EXPOSE 80

FROM node:24-alpine
RUN apk add --no-cache postgresql-client
WORKDIR /app
COPY . .
CMD ["node", "--experimental-strip-types", "app.mjs"]

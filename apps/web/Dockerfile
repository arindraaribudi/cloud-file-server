# syntax=docker/dockerfile:1.7
FROM oven/bun:1.4 AS build
WORKDIR /src
ENV CI=1
COPY package.json bun.lock* ./
RUN bun install
COPY . .
RUN bun run build

FROM gcr.io/distroless/base-debian12:nonroot
WORKDIR /app
# NODE_ENV is injected at runtime by the deployment (k8s/compose).
COPY --from=build /usr/local/bin/bun /usr/local/bin/bun
COPY --from=build /src/dist /app/dist
COPY --from=build /src/server.js /app/server.js
EXPOSE 9001
USER nonroot
CMD ["/usr/local/bin/bun", "run", "server.js"]
# Builds the portal and bakes it into the nginx image.
#
# Build context is the repository root so this can reach ../frontend; the nginx config
# itself stays a bind mount, so it can be edited and reloaded without a rebuild.

FROM node:22-alpine AS build
WORKDIR /app

# Dependencies first, so a source-only change does not reinstall them.
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend/ ./
RUN npm run build

FROM nginx:alpine
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80 443

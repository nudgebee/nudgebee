FROM mcr.microsoft.com/playwright:v1.43.1-jammy
WORKDIR /app
# Copy dependency files
COPY package.json package-lock.json ./
# Install dependencies
RUN npm ci
# Copy rest of the test code
COPY . .
# Ensure Playwright browsers are installed
RUN npx playwright install --with-deps
ENV NODE_ENV=production
CMD ["npx", "playwright", "test"]

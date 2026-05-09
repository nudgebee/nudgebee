// jest.config.js
const nextJest = require('next/jest');

const createJestConfig = nextJest({
  dir: './', // path to your Next.js app
});

const customJestConfig = {
  moduleNameMapper: {
    // Map your custom folders inside src
    '^@components1/(.*)$': '<rootDir>/src/components1/$1',
    '^@api1/(.*)$': '<rootDir>/src/api1/$1',
    '^@assets$': '<rootDir>/src/assets/images',
    '^@assets/(.*)$': '<rootDir>/src/assets/images/$1',
    '^@context/(.*)$': '<rootDir>/src/context/$1',
    '^@data/(.*)$': '<rootDir>/src/data/$1',
    '^@hooks/(.*)$': '<rootDir>/src/hooks/$1',
    '^@lib/(.*)$': '<rootDir>/src/lib/$1',
    '^@pages/(.*)$': '<rootDir>/src/pages/$1',
    '^@styles/(.*)$': '<rootDir>/src/styles/$1',
    '^@utils/(.*)$': '<rootDir>/src/utils/$1',
    '^@common/(.*)$': '<rootDir>/src/components1/common/$1',

    // Handle bare 'src/utils/...' imports used in some components
    '^src/utils/(.*)$': '<rootDir>/src/utils/$1',
    '^src/(.*)$': '<rootDir>/src/$1',

    // Handle CSS modules
    '^.+\\.module\\.(css|sass|scss)$': 'identity-obj-proxy',

    // Handle static assets (e.g., ArrowRightWhiteIcon)
    '\\.(jpg|jpeg|png|gif|webp|avif|svg)$': '<rootDir>/__mocks__/fileMock.js',
  },
  testEnvironment: 'jest-environment-jsdom',
  testPathIgnorePatterns: ['<rootDir>/.next/', '<rootDir>/node_modules/'],
  setupFilesAfterEnv: ['<rootDir>/jest.setup.js'],

  // Optional: coverage
  collectCoverageFrom: [
    'src/components1/**/*.{js,jsx,ts,tsx}',
    'src/pages/**/*.{js,jsx,ts,tsx}',
    '!src/pages/_app.{js,jsx,ts,tsx}',
    '!src/pages/_document.{js,jsx,ts,tsx}',
  ],
};

module.exports = createJestConfig(customJestConfig);

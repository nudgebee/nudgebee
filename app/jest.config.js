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

// next/jest's `createJestConfig` injects its own `transformIgnorePatterns`
// that ignores all of node_modules — including pure-ESM packages that need
// transformation. We override by resolving the async config and rewriting
// the field. Currently only `uuid@14+` requires this (ships `"type":
// "module"` with bare `export` statements). The misleading symptom: jest
// reports a SyntaxError pointing at the first .ts that transitively
// imports the ESM dep, not the dep itself. Add more package names to the
// alternation here if other ESM-only deps land.
module.exports = async () => {
  const baseConfig = await createJestConfig(customJestConfig)();
  // Add `uuid` (and any future ESM-only deps) to the allowlist of every
  // node_modules-matching pattern that next/jest generated. `transformIgnorePatterns`
  // is OR-matched — a file is ignored if ANY pattern matches — so adding a
  // single allow-uuid pattern is not enough; the next/jest-generated patterns
  // would still match. Splice the allowlist into every existing
  // `node_modules` regex.
  const allowList = ['uuid'];
  const allowAlt = allowList.join('|');
  const allowGuard = `(?!(${allowAlt})/)(?!.*node_modules[\\\\/](${allowAlt})[\\\\/])`;
  return {
    ...baseConfig,
    transformIgnorePatterns: (baseConfig.transformIgnorePatterns ?? []).map((p) => {
      if (typeof p !== 'string' || !p.includes('node_modules')) return p;
      // Inject the allowlist guard right after the first `node_modules`
      // separator so the negative lookahead survives the next/jest pattern.
      return p.replace(/node_modules[\\\\\/]/, (match) => `${match}${allowGuard}`);
    }),
  };
};

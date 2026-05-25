function buildUser(n: number) {
  return {
    first: process.env[`USER_${n}_FIRST`] ?? "",
    last: process.env[`USER_${n}_LAST`] ?? "",
    email: process.env[`USER_${n}_EMAIL`] ?? "",
    role: process.env[`USER_${n}_ROLE`] ?? "",
  };
}

// Intentionally only 3 users: all active User-spec tests use at most users[0]–users[2].
// Verified: no test file references users[3] or beyond.
export const users = [1, 2, 3].map(buildUser);

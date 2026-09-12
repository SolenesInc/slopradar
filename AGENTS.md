# Working agreement

- Automated tests are required. Avoid tests whose behavior is already covered
  by the compiler and tests that only verify mocks.
- Tests wait on real signals. Do not use sleeps or polling.
- Every number in code or documentation includes the measurement or source that
  justifies it.
- Do not add prose comments. Prefer names and structure that explain the code.
- Identical input trees must produce identical output bytes.


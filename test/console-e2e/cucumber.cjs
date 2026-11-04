// Run with `npm test` in this directory. SHADOWMOCK_BIN names a built binary; without it the tests build one.
// HEADED=1 shows the browser; CONSOLE_E2E_TAGS narrows the run, such as "@sessions".
module.exports = {
  default: {
    paths: ['features/**/*.feature'],
    require: ['support/**/*.cjs', 'steps/**/*.cjs'],
    format: ['progress-bar', 'summary'],
    tags: process.env.CONSOLE_E2E_TAGS || 'not @wip',
    publishQuiet: true,
  },
};

const assert = require("node:assert/strict")
const fs = require("node:fs")
const os = require("node:os")
const path = require("node:path")
const test = require("node:test")

const {githubCommentMaxUTF16CodeUnits} = require("./report-bounds.cjs")
const upsertComment = require("./upsert-comment.cjs")

const runUrl = "https://github.com/SolenesInc/slopradar/actions/runs/1"

function issueAPI() {
  const comments = []
  return {
    comments,
    github: {
      paginate: async () => comments,
      rest: {
        issues: {
          listComments: async () => ({data: comments}),
          createComment: async ({body}) => {
            const comment = {
              id: "slopradar-comment",
              body,
              html_url: "https://example.invalid/comment",
              user: {login: "github-actions[bot]", type: "Bot"},
            }
            comments.push(comment)
            return {data: comment}
          },
          updateComment: async ({comment_id, body}) => {
            const comment = comments.find(({id}) => id === comment_id)
            comment.body = body
            return {data: comment}
          },
        },
      },
    },
  }
}

function pullRequestContext(headRepository = "SolenesInc/slopradar") {
  return {
    repo: {owner: "SolenesInc", repo: "slopradar"},
    payload: {pull_request: {number: 1, head: {repo: {full_name: headRepository}}}},
  }
}

test("preserves a human marker and creates then updates the bot marker", async (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "slopradar-comment-"))
  t.after(() => fs.rmSync(directory, {recursive: true}))
  const reportPath = path.join(directory, "report.md")
  const api = issueAPI()
  api.comments.push({
    id: "human-comment",
    body: "<!-- slopradar -->\nhuman-authored note\n",
    html_url: "https://example.invalid/human-comment",
    user: {login: "victor", type: "User"},
  })
  const messages = []
  const core = {info: (message) => messages.push(message)}

  fs.writeFileSync(reportPath, "<!-- slopradar -->\nfirst report\n")
  await upsertComment({github: api.github, context: pullRequestContext(), core, reportPath, commentEnabled: "true"})
  fs.writeFileSync(reportPath, "<!-- slopradar -->\nupdated report\n")
  await upsertComment({github: api.github, context: pullRequestContext(), core, reportPath, commentEnabled: "true"})

  assert.equal(api.comments.length, 2)
  assert.equal(api.comments[0].body, "<!-- slopradar -->\nhuman-authored note\n")
  assert.equal(api.comments[1].body, "<!-- slopradar -->\nupdated report\n")
  assert.deepEqual(messages, [
    "slopradar comment created: https://example.invalid/comment",
    "slopradar comment updated: https://example.invalid/comment",
  ])
})

test("keeps the report but skips a fork comment", async (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "slopradar-comment-"))
  t.after(() => fs.rmSync(directory, {recursive: true}))
  const reportPath = path.join(directory, "report.md")
  fs.writeFileSync(reportPath, "<!-- slopradar -->\nreport\n")
  const api = issueAPI()
  const messages = []

  await upsertComment({
    github: api.github,
    context: pullRequestContext("contributor/slopradar"),
    core: {info: (message) => messages.push(message)},
    reportPath,
    commentEnabled: "true",
  })

  assert.equal(api.comments.length, 0)
  assert.match(messages[0], /GITHUB_TOKEN is read-only for fork pull requests/)
})

test("makes a comment permission failure actionable", async (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "slopradar-comment-"))
  t.after(() => fs.rmSync(directory, {recursive: true}))
  const reportPath = path.join(directory, "report.md")
  fs.writeFileSync(reportPath, "<!-- slopradar -->\nreport\n")
  const api = issueAPI()
  api.github.rest.issues.createComment = async () => {
    const error = new Error("Resource not accessible by integration")
    error.status = 403
    throw error
  }

  await assert.rejects(
    upsertComment({
      github: api.github,
      context: pullRequestContext(),
      core: {info: () => {}},
      reportPath,
      commentEnabled: "true",
    }),
    /HTTP 403; ensure the workflow grants pull-requests: write/,
  )
})

test("upserts a bounded structural comment while preserving the report file", async (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "slopradar-comment-"))
  t.after(() => fs.rmSync(directory, {recursive: true}))
  const reportPath = path.join(directory, "report.md")
  const headline = "<!-- slopradar -->\n## slopradar\n\n```diff\n+ 12.000 mass added\n```\n"
  const report = `${headline}\n<details>\n${"x".repeat(githubCommentMaxUTF16CodeUnits)}\n</details>\n`
  fs.writeFileSync(reportPath, report)
  const api = issueAPI()
  const warnings = []

  await upsertComment({
    github: api.github,
    context: pullRequestContext(),
    core: {info: () => {}, warning: (message) => warnings.push(message)},
    reportPath,
    commentEnabled: "true",
    runUrl,
  })

  assert.equal(fs.readFileSync(reportPath, "utf8"), report)
  assert.equal(api.comments.length, 1)
  assert.ok(api.comments[0].body.startsWith(headline))
  assert.doesNotMatch(api.comments[0].body, /<details>/)
  assert.ok(api.comments[0].body.length <= githubCommentMaxUTF16CodeUnits)
  assert.match(warnings[0], /max_comment_utf16_code_units=65536/)
})

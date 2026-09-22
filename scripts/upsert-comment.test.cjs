const assert = require("node:assert/strict")
const fs = require("node:fs")
const os = require("node:os")
const path = require("node:path")
const test = require("node:test")

const {githubCommentMaxUTF16CodeUnits} = require("./report-bounds.cjs")
const upsertComment = require("./upsert-comment.cjs")

const runUrl = "https://github.com/SolenesInc/slopradar/actions/runs/1"

const botAuthor = {__typename: "Bot", login: "github-actions"}

function issueAPI({pageSize = Infinity} = {}) {
  const comments = []
  const writes = []
  let nextId = 1
  return {
    comments,
    writes,
    github: {
      graphql: async (_query, {after}) => {
        const start = after === null ? 0 : Number(after)
        const end = Math.min(start + pageSize, comments.length)
        return {
          repository: {
            pullRequest: {
              headRefOid: "current-head",
              comments: {
                nodes: comments.slice(start, end),
                pageInfo: {hasNextPage: end < comments.length, endCursor: String(end)},
              },
            },
          },
        }
      },
      rest: {
        issues: {
          createComment: async ({body}) => {
            writes.push("create")
            const comment = {
              databaseId: nextId++,
              body,
              url: "https://example.invalid/comment",
              author: botAuthor,
            }
            comments.push(comment)
            return {data: {html_url: comment.url}}
          },
          updateComment: async ({comment_id, body}) => {
            writes.push("update")
            const comment = comments.find(({databaseId}) => databaseId === comment_id)
            comment.body = body
            return {data: {html_url: comment.url}}
          },
        },
      },
    },
  }
}

function writeReport(t, body) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "slopradar-comment-"))
  t.after(() => fs.rmSync(directory, {recursive: true}))
  const reportPath = path.join(directory, "report.md")
  fs.writeFileSync(reportPath, body)
  return reportPath
}

function pullRequestContext(headRepository = "SolenesInc/slopradar", author = "victor") {
  return {
    repo: {owner: "SolenesInc", repo: "slopradar"},
    payload: {pull_request: {number: 1, head: {sha: "current-head", repo: {full_name: headRepository}}, user: {login: author}}},
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
    url: "https://example.invalid/human-comment",
    author: {__typename: "User", login: "victor"},
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

test("keeps the report but skips a same-repository Dependabot comment", async (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "slopradar-comment-"))
  t.after(() => fs.rmSync(directory, {recursive: true}))
  const reportPath = path.join(directory, "report.md")
  const report = "<!-- slopradar -->\nreport\n"
  fs.writeFileSync(reportPath, report)
  const api = issueAPI()
  const messages = []

  await upsertComment({
    github: api.github,
    context: pullRequestContext("SolenesInc/slopradar", "dependabot[bot]"),
    core: {info: (message) => messages.push(message)},
    reportPath,
    commentEnabled: "true",
  })

  assert.equal(fs.readFileSync(reportPath, "utf8"), report)
  assert.equal(api.comments.length, 0)
  assert.match(messages[0], /Dependabot pull request workflows a read-only GITHUB_TOKEN/)
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

test("stale runs neither create a duplicate nor overwrite the current report", async (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "slopradar-stale-"))
  t.after(() => fs.rmSync(directory, {recursive: true}))
  const reportPath = path.join(directory, "report.md")
  fs.writeFileSync(reportPath, "<!-- slopradar -->\ncurrent report\n")
  const api = issueAPI()
  const messages = []
  const core = {info: (message) => messages.push(message)}
  const stale = pullRequestContext()
  stale.payload.pull_request.head.sha = "previous-head"
  await upsertComment({github: api.github, context: stale, core, reportPath, commentEnabled: "true"})
  assert.equal(api.comments.length, 0)
  await upsertComment({github: api.github, context: pullRequestContext(), core, reportPath, commentEnabled: "true"})
  fs.writeFileSync(reportPath, "<!-- slopradar -->\nstale report\n")
  await upsertComment({github: api.github, context: stale, core, reportPath, commentEnabled: "true"})
  assert.equal(api.comments.length, 1)
  assert.equal(api.comments[0].body, "<!-- slopradar -->\ncurrent report\n")
  assert.equal(messages.filter((message) => message.includes("differs from current PR head")).length, 2)
})

test("updates a bot comment found beyond the first page instead of duplicating it", async (t) => {
  const reportPath = writeReport(t, "<!-- slopradar -->\nnew report\n")
  const api = issueAPI({pageSize: 2})
  for (let index = 0; index < 5; index++) {
    api.comments.push({databaseId: 100 + index, body: `review ${index}`, url: "https://example.invalid/review", author: botAuthor})
  }
  api.comments.push({databaseId: 200, body: "<!-- slopradar -->\nold report\n", url: "https://example.invalid/comment", author: botAuthor})

  await upsertComment({github: api.github, context: pullRequestContext(), core: {info: () => {}}, reportPath, commentEnabled: "true"})

  assert.equal(api.comments.length, 6)
  assert.equal(api.comments[5].body, "<!-- slopradar -->\nnew report\n")
  assert.deepEqual(api.writes, ["update"])
})

test("leaves an identical comment untouched", async (t) => {
  const reportPath = writeReport(t, "<!-- slopradar -->\nreport\n")
  const api = issueAPI()
  const messages = []
  const core = {info: (message) => messages.push(message)}

  await upsertComment({github: api.github, context: pullRequestContext(), core, reportPath, commentEnabled: "true"})
  await upsertComment({github: api.github, context: pullRequestContext(), core, reportPath, commentEnabled: "true"})

  assert.deepEqual(api.writes, ["create"])
  assert.equal(messages[1], "slopradar comment unchanged: https://example.invalid/comment")
})

test("warns instead of failing when the REST rate limit is exhausted", async (t) => {
  const reportPath = writeReport(t, "<!-- slopradar -->\nreport\n")
  const api = issueAPI()
  api.github.rest.issues.createComment = async () => {
    const error = new Error("API rate limit exceeded for installation.")
    error.status = 403
    throw error
  }
  const warnings = []

  await upsertComment({
    github: api.github,
    context: pullRequestContext(),
    core: {info: () => {}, warning: (message) => warnings.push(message)},
    reportPath,
    commentEnabled: "true",
    runUrl,
  })

  assert.equal(warnings.length, 1)
  assert.match(warnings[0], /rate limit is exhausted\. The report is in the job summary: https:\/\/github\.com\/SolenesInc\/slopradar\/actions\/runs\/1/)
})

test("warns instead of failing when the GraphQL rate limit is exhausted", async (t) => {
  const reportPath = writeReport(t, "<!-- slopradar -->\nreport\n")
  const api = issueAPI()
  api.github.graphql = async () => {
    const error = new Error("Request failed due to following response errors:\n - API rate limit exceeded for installation.")
    error.errors = [{type: "RATE_LIMITED", message: "API rate limit exceeded for installation."}]
    throw error
  }
  const warnings = []

  await upsertComment({
    github: api.github,
    context: pullRequestContext(),
    core: {info: () => {}, warning: (message) => warnings.push(message)},
    reportPath,
    commentEnabled: "true",
    runUrl,
  })

  assert.deepEqual(api.writes, [])
  assert.equal(warnings.length, 1)
})

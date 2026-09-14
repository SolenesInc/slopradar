const assert = require("node:assert/strict")
const fs = require("node:fs")
const os = require("node:os")
const path = require("node:path")
const test = require("node:test")

const publishSummary = require("./publish-summary.cjs")
const {
  commentForReport,
  githubCommentMaxUTF16CodeUnits,
  githubSummaryMaxBytes,
  marker,
  summaryForReport,
} = require("./report-bounds.cjs")

const runUrl = "https://github.com/SolenesInc/slopradar/actions/runs/1"
const artifactName = "slopradar-report-test"
const headline = `${marker}\n## slopradar\n\n\`\`\`diff\n+ 12.000 mass added\n\`\`\`\n`

function asciiReport(length) {
  return marker + "x".repeat(length - marker.length)
}

test("uses the GitHub comment boundary in UTF-16 code units", () => {
  const atLimit = asciiReport(githubCommentMaxUTF16CodeUnits)
  assert.deepEqual(commentForReport(atLimit, runUrl), {
    body: atLimit,
    overflow: false,
    asked: githubCommentMaxUTF16CodeUnits,
  })

  const overLimit = asciiReport(githubCommentMaxUTF16CodeUnits + 1)
  const bounded = commentForReport(overLimit, runUrl)
  assert.equal(bounded.overflow, true)
  assert.equal(bounded.asked, githubCommentMaxUTF16CodeUnits + 1)
  assert.ok(bounded.body.length <= githubCommentMaxUTF16CodeUnits)
  assert.match(bounded.body, /max_comment_utf16_code_units=65536/)
  assert.match(bounded.body, /asked_comment_utf16_code_units=65537/)
  assert.equal(commentForReport(`${marker}\n😀`, runUrl).asked, marker.length + 3)
})

test("keeps the complete headline and drops whole details on comment overflow", () => {
  const report = `${headline}\n<details>\n${"x".repeat(githubCommentMaxUTF16CodeUnits)}\n</details>\n`
  const bounded = commentForReport(report, runUrl)
  assert.ok(bounded.body.startsWith(headline))
  assert.doesNotMatch(bounded.body, /<details>/)
  assert.ok(bounded.body.includes(runUrl))
})

test("uses a short comment notice when the structural prefix cannot fit", () => {
  const oversizedFence = `${marker}\n## slopradar\n\n\`\`\`text\n${"x".repeat(githubCommentMaxUTF16CodeUnits)}\n\`\`\`\n\n<details>\nbody\n</details>\n`
  const bounded = commentForReport(oversizedFence, runUrl)
  assert.ok(bounded.body.startsWith(`${marker}\n## slopradar\n`))
  assert.doesNotMatch(bounded.body, /```/)
  assert.ok(bounded.body.length <= githubCommentMaxUTF16CodeUnits)
})

test("uses the GitHub step summary boundary in UTF-8 bytes", () => {
  const atLimit = asciiReport(githubSummaryMaxBytes)
  assert.deepEqual(summaryForReport(atLimit, runUrl, artifactName), {
    body: atLimit,
    overflow: false,
    asked: githubSummaryMaxBytes,
  })

  const overLimit = asciiReport(githubSummaryMaxBytes + 1)
  const bounded = summaryForReport(overLimit, runUrl, artifactName)
  assert.equal(bounded.overflow, true)
  assert.equal(bounded.asked, githubSummaryMaxBytes + 1)
  assert.ok(Buffer.byteLength(bounded.body, "utf8") <= githubSummaryMaxBytes)
  assert.match(bounded.body, /max_summary_bytes=1048576/)
  assert.match(bounded.body, /asked_summary_bytes=1048577/)
})

test("keeps the complete headline and drops whole details on summary overflow", () => {
  const report = `${headline}\n<details>\n${"😀".repeat(githubSummaryMaxBytes)}\n</details>\n`
  const bounded = summaryForReport(report, runUrl, artifactName)
  assert.ok(bounded.body.startsWith(headline))
  assert.doesNotMatch(bounded.body, /<details>/)
  assert.match(bounded.body, new RegExp(artifactName))
  assert.ok(Buffer.byteLength(bounded.body, "utf8") <= githubSummaryMaxBytes)
})

test("uses a short summary notice when the structural prefix cannot fit", () => {
  const oversizedFence = `${marker}\n## slopradar\n\n\`\`\`text\n${"x".repeat(githubSummaryMaxBytes)}\n\`\`\`\n\n<details>\nbody\n</details>\n`
  const bounded = summaryForReport(oversizedFence, runUrl, artifactName)
  assert.ok(bounded.body.startsWith(`${marker}\n## slopradar\n`))
  assert.doesNotMatch(bounded.body, /```/)
  assert.ok(Buffer.byteLength(bounded.body, "utf8") <= githubSummaryMaxBytes)
})

test("publishing an overflow summary preserves the full report file", async (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "slopradar-summary-"))
  t.after(() => fs.rmSync(directory, {recursive: true}))
  const reportPath = path.join(directory, "report.md")
  const report = asciiReport(githubSummaryMaxBytes + 1)
  fs.writeFileSync(reportPath, report)
  const outputs = {}
  const warnings = []
  let published
  const summary = {
    addRaw: (body) => {
      published = body
      return summary
    },
    write: async () => {},
  }

  await publishSummary({
    core: {
      setOutput: (name, value) => {
        outputs[name] = value
      },
      summary,
      warning: (message) => warnings.push(message),
    },
    reportPath,
    runUrl,
    artifactName,
  })

  assert.equal(fs.readFileSync(reportPath, "utf8"), report)
  assert.equal(outputs.overflow, "true")
  assert.equal(outputs["artifact-name"], artifactName)
  assert.ok(Buffer.byteLength(published, "utf8") <= githubSummaryMaxBytes)
  assert.match(warnings[0], /preserving the full report/)
})

const marker = "<!-- slopradar -->"
const githubCommentMaxUTF16CodeUnits = 65536
const githubSummaryMaxBytes = 1024 * 1024

function reportPrefix(report) {
  const details = report.indexOf("\n<details>")
  return details === -1 ? report : report.slice(0, details + 1)
}

function fitStructuralPrefix(report, notice, max, length) {
  const prefix = reportPrefix(report)
  const candidate = `${prefix}${prefix.endsWith("\n") ? "" : "\n"}\n${notice}\n`
  if (length(candidate) <= max) {
    return candidate
  }
  const short = `${marker}\n## slopradar\n\n${notice}\n`
  if (length(short) > max) {
    throw new Error(`slopradar overflow notice exceeds its destination limit: max=${max}, asked=${length(short)}`)
  }
  return short
}

function commentForReport(report, runUrl) {
  if (!report.startsWith(marker)) {
    throw new Error(`slopradar report must start with ${marker}`)
  }
  const asked = report.length
  if (asked <= githubCommentMaxUTF16CodeUnits) {
    return {body: report, overflow: false, asked}
  }
  const notice = `> Full report exceeds the GitHub pull request comment limit (\`max_comment_utf16_code_units=${githubCommentMaxUTF16CodeUnits}\`, \`asked_comment_utf16_code_units=${asked}\`). [Open the workflow run for the full report](${runUrl}).`
  return {
    body: fitStructuralPrefix(report, notice, githubCommentMaxUTF16CodeUnits, (value) => value.length),
    overflow: true,
    asked,
  }
}

function summaryForReport(report, runUrl, artifactName) {
  if (!report.startsWith(marker)) {
    throw new Error(`slopradar report must start with ${marker}`)
  }
  const asked = Buffer.byteLength(report, "utf8")
  if (asked <= githubSummaryMaxBytes) {
    return {body: report, overflow: false, asked}
  }
  const notice = `> Full report exceeds the GitHub step summary limit (\`max_summary_bytes=${githubSummaryMaxBytes}\`, \`asked_summary_bytes=${asked}\`). Download the lossless Markdown from \`${artifactName}\` on [this workflow run](${runUrl}).`
  return {
    body: fitStructuralPrefix(
      report,
      notice,
      githubSummaryMaxBytes,
      (value) => Buffer.byteLength(value, "utf8"),
    ),
    overflow: true,
    asked,
  }
}

module.exports = {
  commentForReport,
  githubCommentMaxUTF16CodeUnits,
  githubSummaryMaxBytes,
  marker,
  summaryForReport,
}

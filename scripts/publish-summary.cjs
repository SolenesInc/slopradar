const fs = require("node:fs")

const {githubSummaryMaxBytes, summaryForReport} = require("./report-bounds.cjs")

module.exports = async function publishSummary({core, reportPath, runUrl, artifactName}) {
  const report = fs.readFileSync(reportPath, "utf8")
  const summary = summaryForReport(report, runUrl, artifactName)
  core.setOutput("overflow", String(summary.overflow))
  core.setOutput("artifact-name", artifactName)
  if (summary.overflow) {
    core.warning(
      `slopradar report exceeds the step summary limit: max_summary_bytes=${githubSummaryMaxBytes}, asked_summary_bytes=${summary.asked}; preserving the full report as ${artifactName}`,
    )
  }
  await core.summary.addRaw(summary.body).write()
}

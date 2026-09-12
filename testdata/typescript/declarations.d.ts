export interface Reporter {
  report(): void
}

export declare function createReporter(): Reporter
export const reporter: Reporter

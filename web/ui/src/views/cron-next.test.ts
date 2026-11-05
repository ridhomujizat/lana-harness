import { describe, expect, it } from "vitest";
import { nextCron, parseCron } from "./cron-next";

/* The Schedules page computes the next fire time like internal/workflow/cron.go. */
describe("nextCron", () => {
  it("finds the next 07:00 in Asia/Jakarta", () => {
    /* 2026-11-05 06:30 Jakarta = 2026-11-04 23:30 UTC. */
    const after = Date.UTC(2026, 10, 4, 23, 30);
    expect(nextCron("0 7 * * *", "Asia/Jakarta", after)).toBe(Date.UTC(2026, 10, 5, 0, 0));
  });

  it("rolls to the next day after the time passed", () => {
    const after = Date.UTC(2026, 10, 5, 0, 0);
    expect(nextCron("0 7 * * *", "Asia/Jakarta", after)).toBe(Date.UTC(2026, 10, 6, 0, 0));
  });

  it("handles steps and weekdays", () => {
    const after = Date.UTC(2026, 10, 5, 10, 7);
    expect(nextCron("*/15 * * * *", "UTC", after)).toBe(Date.UTC(2026, 10, 5, 10, 15));
    /* 2026-11-05 is a Thursday; the next Monday 08:00 UTC is 2026-11-09. */
    expect(nextCron("0 8 * * mon", "UTC", after)).toBe(Date.UTC(2026, 10, 9, 8, 0));
  });

  it("rejects a bad expression", () => {
    expect(parseCron("61 * * * *")).toBeNull();
    expect(nextCron("nope", "UTC", 0)).toBe(0);
  });
});

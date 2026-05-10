import { describe, expect, it } from "vitest";
import type { TaskMessagePayload } from "@multica/core/types";
import { pickStageKeys } from "./task-status-pill";

const NO_MSGS: readonly TaskMessagePayload[] = [];

describe("pickStageKeys", () => {
  describe("queued / dispatched + presence", () => {
    it("offline + queued → static offline label (unambiguous runtime-down state)", () => {
      expect(pickStageKeys("queued", NO_MSGS, "offline", 5)).toEqual({
        stageKey: "offline",
        static: true,
      });
    });

    it("offline + dispatched → static offline (same runtime-down treatment)", () => {
      expect(pickStageKeys("dispatched", NO_MSGS, "offline", 5)).toEqual({
        stageKey: "offline",
        static: true,
      });
    });

    it("unstable + queued → reconnecting (transient amber state, not stuck)", () => {
      expect(pickStageKeys("queued", NO_MSGS, "unstable", 5)).toEqual({
        stageKey: "reconnecting",
      });
    });
  });

  describe("stuck-detection while runtime appears online", () => {
    // Reproduction of the GH #2341 footgun: backend has not yet swept the
    // dead daemon, so availability is "online" while the task sits queued
    // forever. The 30s threshold gives the user a diagnostic cue well
    // before the backend's ~150s sweep window expires.

    it("queued + online + elapsed < 30s → normal queued (brief queueing is healthy)", () => {
      expect(pickStageKeys("queued", NO_MSGS, "online", 5)).toEqual({
        stageKey: "queued",
      });
    });

    it("queued + online + elapsed exactly 30s → flips to static stuck", () => {
      expect(pickStageKeys("queued", NO_MSGS, "online", 30)).toEqual({
        stageKey: "stuck",
        static: true,
      });
    });

    it("dispatched + online + elapsed > 30s → static stuck (daemon claimed but never started)", () => {
      expect(pickStageKeys("dispatched", NO_MSGS, "online", 60)).toEqual({
        stageKey: "stuck",
        static: true,
      });
    });

    it("queued + undefined availability + elapsed > 30s → still stuck (presence still loading shouldn't hide the cue)", () => {
      // Loading presence shouldn't gate the stuck warning; if the user has
      // been waiting longer than the threshold, the cue applies regardless.
      expect(pickStageKeys("queued", NO_MSGS, undefined, 45)).toEqual({
        stageKey: "stuck",
        static: true,
      });
    });

    it("offline always wins over stuck (clearer copy + the stuck label would be redundant)", () => {
      // Even when elapsed is well past the stuck threshold, an offline
      // runtime gets the "Runtime offline" label — it's a more specific
      // diagnosis than the generic stuck cue.
      expect(pickStageKeys("queued", NO_MSGS, "offline", 120)).toEqual({
        stageKey: "offline",
        static: true,
      });
    });
  });

  describe("running stage decisions are unaffected by elapsed", () => {
    it("running + no messages → thinking", () => {
      expect(pickStageKeys("running", NO_MSGS, "online", 5)).toEqual({
        stageKey: "thinking",
      });
    });

    it("running + text message → typing (and the stuck threshold doesn't fire)", () => {
      const msgs: TaskMessagePayload[] = [
        {
          task_id: "t1",
          issue_id: "",
          seq: 1,
          type: "text",
          content: "hi",
        },
      ];
      expect(pickStageKeys("running", msgs, "online", 999)).toEqual({
        stageKey: "typing",
      });
    });
  });
});

import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { describe, expect, test } from "vitest";

import {
  AcknowledgementSchema,
  ErrorCode,
  ErrorSchema,
  EnvelopeSchema,
  PongSchema,
  ProcessExitOutcome,
  ProcessExitSchema,
  ResumeDisposition,
  SessionStatusSchema,
  TerminalOutputSchema,
  type Envelope,
} from "../gen/vibebridge/v1/envelope_pb";
import {
  ProtocolV1ClientStream,
  protocolV1MaxEnvelopeBytes,
  protocolV1MaxTerminalDimension,
} from "./protocol-v1";

const connectionId = Uint8Array.from({ length: 16 }, (_, index) => index);
const sentAt = new Date("2026-07-13T12:00:00Z");

function agentEnvelope(sequence: bigint, acknowledge: bigint, payload: Envelope["payload"], sessionId = new Uint8Array(), sessionGeneration = 0n) {
  return toBinary(EnvelopeSchema, create(EnvelopeSchema, {
    protocolMajor: 1,
    connectionId,
    sessionId,
    sessionGeneration,
    sequence,
    acknowledge,
    sentAt: timestampFromDate(sentAt),
    payload,
  }));
}

describe("Protocol V1 sequenced terminal stream", () => {
  test("sequences terminal input, output, and acknowledgements", () => {
    const stream = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes);

    const input = fromBinary(EnvelopeSchema, stream.createTerminalInput("yes\r", sentAt));
    expect(input.sequence).toBe(2n);
    expect(input.acknowledge).toBe(1n);
    expect(input.payload.case).toBe("terminalInput");
    if (input.payload.case !== "terminalInput") throw new Error("expected terminal input");
    expect(new TextDecoder().decode(input.payload.value.data)).toBe("yes\r");

    const output = agentEnvelope(2n, 2n, {
      case: "terminalOutput",
      value: create(TerminalOutputSchema, { data: new TextEncoder().encode("ready\r\n") }),
    });
    const message = stream.acceptAgentMessage(output);
    expect(message.type).toBe("terminal-output");
    if (message.type !== "terminal-output") throw new Error("expected terminal output");
    expect(Array.from(message.data)).toEqual(Array.from(new TextEncoder().encode("ready\r\n")));

    const acknowledgement = fromBinary(EnvelopeSchema, stream.createAcknowledgement(sentAt));
    expect(acknowledgement.sequence).toBe(3n);
    expect(acknowledgement.acknowledge).toBe(2n);
    expect(acknowledgement.payload.case).toBe("acknowledgement");
  });

  test("sequences a negotiated application health check", () => {
    const stream = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, { controlHealth: true });

    const ping = fromBinary(EnvelopeSchema, stream.createPing(sentAt));
    expect(ping.sequence).toBe(2n);
    expect(ping.acknowledge).toBe(1n);
    expect(ping.payload.case).toBe("ping");
    expect(stream.usesControlHealth()).toBe(true);

    const pong = agentEnvelope(2n, 2n, {
      case: "pong",
      value: create(PongSchema),
    });
    expect(stream.acceptAgentMessage(pong)).toEqual({ type: "pong" });
    expect(() => stream.acceptAgentMessage(agentEnvelope(3n, 2n, {
      case: "pong",
      value: create(PongSchema),
    }))).toThrow("outstanding Ping");

    const insufficientAcknowledgement = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, { controlHealth: true });
    insufficientAcknowledgement.createPing(sentAt);
    expect(() => insufficientAcknowledgement.acceptAgentMessage(agentEnvelope(2n, 1n, {
      case: "pong",
      value: create(PongSchema),
    }))).toThrow("outstanding Ping");

    const unsolicited = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, { controlHealth: true });
    expect(() => unsolicited.acceptAgentMessage(agentEnvelope(2n, 1n, {
      case: "pong",
      value: create(PongSchema),
    }))).toThrow("outstanding Ping");

    const unnegotiated = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes);
    expect(() => unnegotiated.createPing(sentAt)).toThrow("not negotiated");
    expect(() => unnegotiated.acceptAgentMessage(agentEnvelope(2n, 1n, {
      case: "pong",
      value: create(PongSchema),
    }))).toThrow("Pong");

    const resumable = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, {
      controlHealth: true,
      sessionResume: true,
    });
    resumable.createAttachSession(undefined, sentAt);
    expect(() => resumable.createPing(sentAt)).toThrow("SessionStatus");
  });

  test("sequences negotiated terminal resize and end controls", () => {
    const stream = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, { terminalResizeEnd: true });

    const resize = fromBinary(EnvelopeSchema, stream.createTerminalResize(120, 40, sentAt));
    expect(resize.sequence).toBe(2n);
    expect(resize.acknowledge).toBe(1n);
    expect(resize.payload.case).toBe("terminalResize");
    if (resize.payload.case !== "terminalResize") throw new Error("expected terminal resize");
    expect(resize.payload.value).toMatchObject({ columns: 120, rows: 40 });

    const end = fromBinary(EnvelopeSchema, stream.createEndSession(sentAt));
    expect(end.sequence).toBe(3n);
    expect(end.acknowledge).toBe(1n);
    expect(end.payload.case).toBe("endSession");
  });

  test("rejects unnegotiated and invalid terminal controls", () => {
    const unnegotiated = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes);
    expect(() => unnegotiated.createTerminalResize(80, 24, sentAt)).toThrow("not negotiated");
    expect(() => unnegotiated.createEndSession(sentAt)).toThrow("not negotiated");

    for (const [columns, rows] of [[0, 24], [80, 0], [1.5, 24], [protocolV1MaxTerminalDimension + 1, 24]]) {
      const stream = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, { terminalResizeEnd: true });
      expect(() => stream.createTerminalResize(columns, rows, sentAt)).toThrow("dimensions");
    }
  });

  test("accepts only negotiated process-exit outcomes", () => {
    const stream = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, { sessionProcessExit: true });
    const processExit = agentEnvelope(2n, 1n, {
      case: "processExit",
      value: create(ProcessExitSchema, { outcome: ProcessExitOutcome.SUCCESS }),
    });
    expect(stream.acceptAgentMessage(processExit)).toEqual({ type: "process-exit", outcome: ProcessExitOutcome.SUCCESS });

    const unnegotiated = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes);
    expect(() => unnegotiated.acceptAgentMessage(processExit)).toThrow("ProcessExit");
    const unspecified = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, { sessionProcessExit: true });
    expect(() => unspecified.acceptAgentMessage(agentEnvelope(2n, 1n, {
      case: "processExit",
      value: create(ProcessExitSchema, { outcome: ProcessExitOutcome.UNSPECIFIED }),
    }))).toThrow("ProcessExit");
  });

  test("accepts only negotiated stable errors", () => {
    const error = agentEnvelope(2n, 1n, {
      case: "error",
      value: create(ErrorSchema, { code: ErrorCode.SESSION_START_FAILED }),
    });
    const stream = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, { controlError: true });
    expect(stream.acceptAgentMessage(error)).toEqual({ type: "error", code: ErrorCode.SESSION_START_FAILED });
    expect(stream.usesControlError()).toBe(true);

    const unnegotiated = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes);
    expect(() => unnegotiated.acceptAgentMessage(error)).toThrow("Error");
    for (const code of [ErrorCode.UNSPECIFIED, 99 as ErrorCode]) {
      const invalid = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, { controlError: true });
      expect(() => invalid.acceptAgentMessage(agentEnvelope(2n, 1n, {
        case: "error",
        value: create(ErrorSchema, { code }),
      }))).toThrow("Error");
    }
  });

  test("accepts empty-metadata Error before resume binding without binding the stream", () => {
    const stream = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, {
      controlError: true,
      sessionResume: true,
    });
    stream.createAttachSession(undefined, sentAt);
    const message = stream.acceptAgentMessage(agentEnvelope(2n, 1n, {
      case: "error",
      value: create(ErrorSchema, { code: ErrorCode.SESSION_ALREADY_ACTIVE }),
    }));
    expect(message).toEqual({ type: "error", code: ErrorCode.SESSION_ALREADY_ACTIVE });
    expect(stream.getResumeCursor()).toBeNull();

    for (const code of [ErrorCode.TERMINAL_INPUT_FAILED, ErrorCode.TERMINAL_RESIZE_FAILED, ErrorCode.UNSUPPORTED_MESSAGE]) {
      const invalidCode = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, {
        controlError: true,
        sessionResume: true,
      });
      invalidCode.createAttachSession(undefined, sentAt);
      expect(() => invalidCode.acceptAgentMessage(agentEnvelope(2n, 1n, {
        case: "error",
        value: create(ErrorSchema, { code }),
      }))).toThrow("before SessionStatus");
    }

    const invalidMetadata = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, {
      controlError: true,
      sessionResume: true,
    });
    invalidMetadata.createAttachSession(undefined, sentAt);
    const sessionId = Uint8Array.from({ length: 16 }, (_, index) => 255 - index);
    expect(() => invalidMetadata.acceptAgentMessage(agentEnvelope(2n, 1n, {
      case: "error",
      value: create(ErrorSchema, { code: ErrorCode.SESSION_START_FAILED }),
    }, sessionId, 1n))).toThrow("metadata");
  });

  test("attaches and carries a resumable session identity", () => {
    const sessionId = Uint8Array.from({ length: 16 }, (_, index) => 255 - index);
    const stream = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, { sessionResume: true });

    const attach = fromBinary(EnvelopeSchema, stream.createAttachSession({
      sessionId,
      sessionGeneration: 7n,
      lastAcknowledgedSequence: 9n,
    }, sentAt));
    expect(attach.sequence).toBe(2n);
    expect(attach.acknowledge).toBe(1n);
    expect(attach.sessionId).toEqual(sessionId);
    expect(attach.sessionGeneration).toBe(7n);
    expect(attach.payload.case).toBe("attachSession");
    if (attach.payload.case !== "attachSession") throw new Error("expected AttachSession");
    expect(attach.payload.value.lastAcknowledgedSequence).toBe(9n);

    const status = agentEnvelope(2n, 2n, {
      case: "sessionStatus",
      value: create(SessionStatusSchema, { resumeDisposition: ResumeDisposition.RESUMED }),
    }, sessionId, 7n);
    const statusMessage = stream.acceptAgentMessage(status);
    expect(statusMessage).toEqual({
      type: "session-status",
      disposition: ResumeDisposition.RESUMED,
      sessionId,
      sessionGeneration: 7n,
    });
    expect(stream.getResumeCursor()).toEqual({
      sessionId,
      sessionGeneration: 7n,
      lastAcknowledgedSequence: 2n,
    });

    const output = agentEnvelope(3n, 2n, {
      case: "terminalOutput",
      value: create(TerminalOutputSchema, { data: new TextEncoder().encode("restored\r\n") }),
    }, sessionId, 7n);
    expect(stream.acceptAgentMessage(output).type).toBe("terminal-output");
    expect(stream.getResumeCursor()?.lastAcknowledgedSequence).toBe(3n);

    const input = fromBinary(EnvelopeSchema, stream.createTerminalInput("continue\r", sentAt));
    expect(input.sequence).toBe(3n);
    expect(input.sessionId).toEqual(sessionId);
    expect(input.sessionGeneration).toBe(7n);
  });

  test("rejects stream traffic before SessionStatus and mismatched bound metadata", () => {
    const sessionId = Uint8Array.from({ length: 16 }, (_, index) => 255 - index);
    const stream = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, { sessionResume: true });
    stream.createAttachSession(undefined, sentAt);

    expect(() => stream.acceptAgentMessage(agentEnvelope(2n, 2n, {
      case: "terminalOutput",
      value: create(TerminalOutputSchema, { data: new Uint8Array([1]) }),
    }))).toThrow("SessionStatus");
    expect(() => stream.acceptAgentMessage(agentEnvelope(2n, 2n, {
      case: "sessionStatus",
      value: fromBinary(SessionStatusSchema, new Uint8Array([8, 99])),
    }, sessionId, 1n))).toThrow("SessionStatus");

    stream.acceptAgentMessage(agentEnvelope(2n, 2n, {
      case: "sessionStatus",
      value: create(SessionStatusSchema, { resumeDisposition: ResumeDisposition.FRESH }),
    }, sessionId, 1n));

    expect(() => stream.acceptAgentMessage(agentEnvelope(3n, 2n, {
      case: "acknowledgement",
      value: create(AcknowledgementSchema),
    }, sessionId, 2n))).toThrow("session metadata");
  });

  test.each([
    ["duplicate sequence", 1n, 0n],
    ["sequence gap", 3n, 0n],
    ["acknowledges unsent message", 2n, 2n],
  ])("rejects %s", (_name, sequence, acknowledge) => {
    const stream = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes);
    const encoded = agentEnvelope(sequence, acknowledge, {
      case: "acknowledgement",
      value: create(AcknowledgementSchema),
    });
    expect(() => stream.acceptAgentMessage(encoded)).toThrow();
  });
});

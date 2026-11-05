// SOFCAT_VITE_MOCK_ONLY — this module exists so `npm run dev` and the public
// demo build can render the UI in a plain browser. vite.config.ts resolves it
// only in development and demo modes, and the production-bundle gate greps for
// this marker.
import type { AcceptedOperation, SofCatApi, OperationStatus } from "./api.ts";
import { type MockItem, STORES, UNBRANDED, offline, storeName, unbranded } from "./mock-stores.ts";
import { isTerminalState } from "./state.ts";

const MOCK_MARKER = "SOFCAT_VITE_MOCK_ONLY";
const MOCK_USER = "ACME\\demo";

const store = STORES[storeName()];
const items: MockItem[] = store.items;

const handlers: ((status: OperationStatus) => void)[] = [];
let operationCounter = 0;

function accept(): AcceptedOperation {
  operationCounter += 1;
  return {
    operationId: `${MOCK_MARKER}-op-${operationCounter}`,
    accepted: true,
    queuedAtUtc: new Date().toISOString(),
    requestedBy: MOCK_USER,
  };
}

type MockEvent = [delayMs: number, status: Partial<OperationStatus>];

function sequence(item: MockItem, removing: boolean): MockEvent[] {
  const phase = removing ? "Removing" : "Installing";
  const queued: MockEvent = [0, { state: "Queued", message: "Waiting for the service." }];
  const { outcome, reason = "", dependency } = item.mock ?? {};

  if (outcome === "fail") {
    return [
      queued,
      [400, { state: "Downloading", progressPercent: 40 }],
      [900, { state: "ItemFailed", message: reason }],
      [
        1200,
        {
          state: "Failed",
          message: "run reported a failure",
          errorCode: "install_failed",
          errorMessage: reason,
        },
      ],
    ];
  }

  if (outcome === "missing") {
    return [
      queued,
      [900, { state: "Deferred", message: reason, errorCode: "missing_software", errorMessage: reason }],
    ];
  }

  if (outcome === "blocked") {
    return [
      queued,
      [400, { state: "Downloading", progressPercent: 60 }],
      [900, { state: "Deferred", message: reason, errorCode: "blocked_by_running_app", errorMessage: reason }],
    ];
  }

  // No terminal record: the watch call rejects, exercising the stream_ended UI.
  if (outcome === "no-terminal") {
    return [queued, [400, { state: "Downloading", progressPercent: 30 }]];
  }

  // The download is slow enough to Cancel, which the service allows until
  // the installer starts.
  const events: MockEvent[] = [
    queued,
    [300, { state: "Downloading", progressPercent: 20 }],
    [700, { state: "Downloading", progressPercent: 55 }],
    [1100, { state: "Downloading", progressPercent: 90 }],
  ];
  let at = 1100;
  // A dependency item carries its own identity and resets the percentage.
  if (dependency && !removing) {
    events.push(
      [(at += 400), { state: "Downloading", progressPercent: 10, ...dependency }],
      [(at += 400), { state: "ItemCompleted", progressPercent: 100, ...dependency }],
    );
  }
  // The service measures only downloads; an install or removal has no percent.
  events.push(
    [(at += 400), { state: phase }],
    [(at += 600), { state: "ItemCompleted", progressPercent: 100 }],
    [(at += 400), { state: "Succeeded", message: "run completed" }],
  );
  return events;
}

const acceptedOperations = new Map<string, { item: MockItem; removing: boolean }>();

function mutate(itemName: string, removing: boolean): Promise<AcceptedOperation> {
  const item = items.find((candidate) => candidate.itemName === itemName);
  if (!item) {
    return Promise.reject(new Error(`unknown item ${itemName}`));
  }
  const accepted = accept();
  acceptedOperations.set(accepted.operationId ?? "", { item, removing });
  return Promise.resolve(accepted);
}

// Watched operations, for cancelOperation: pending timers, the state last sent,
// and how to settle the watch call.
type Watched = {
  item: MockItem;
  state: string;
  // seq is the last record number sent, as the service numbers them.
  seq: number;
  timers: ReturnType<typeof setTimeout>[];
  settle: () => void;
};
const watched = new Map<string, Watched>();

function emit(status: OperationStatus): void {
  for (const handler of handlers) {
    handler(status);
  }
}

/**
 * watch replays a sequence through the same callback production uses, so the
 * mock exercises the real routing: nothing is delivered until the UI watches,
 * and a sequence with no terminal record rejects like a premature stream end.
 */
function watch(operationId: string): Promise<void> {
  const request = acceptedOperations.get(operationId);
  if (!request) {
    return Promise.reject(new Error(`unknown operation ${operationId}`));
  }
  acceptedOperations.delete(operationId);
  const { item, removing } = request;
  const events = sequence(item, removing);
  const [lastDelay, lastPartial] = events[events.length - 1];

  return new Promise((resolve, reject) => {
    const entry: Watched = { item, state: "", seq: 0, timers: [], settle: resolve };
    watched.set(operationId, entry);
    for (const [delay, partial] of events) {
      entry.timers.push(
        setTimeout(() => {
          const status: OperationStatus = {
            operationId,
            seq: ++entry.seq,
            timestampUtc: new Date().toISOString(),
            itemName: item.itemName,
            displayName: item.displayName,
            state: "",
            message: "",
            requestedBy: MOCK_USER,
            ...partial,
          };
          if (status.state === "Succeeded") {
            item.isInstalled = !removing;
            item.isManaged = !removing;
            item.status = removing ? "NotInstalled" : "Installed";
          }
          entry.state = status.state;
          emit(status);
        }, delay),
      );
    }
    entry.timers.push(
      setTimeout(() => {
        watched.delete(operationId);
        if (isTerminalState(lastPartial.state ?? "")) {
          resolve();
        } else {
          reject(new Error("operation stream ended before a terminal event"));
        }
      }, lastDelay + 1),
    );
  });
}

/** cancel mirrors the service: accepted until the installer starts, refused after. */
function cancel(operationId: string): Promise<void> {
  const entry = watched.get(operationId);
  if (!entry) {
    return Promise.reject(new Error("operation_not_cancelable: operation can no longer be canceled: it has already finished"));
  }
  if (!["", "Queued", "Downloading"].includes(entry.state)) {
    return Promise.reject(
      new Error(
        `operation_not_cancelable: operation can no longer be canceled: work on ${entry.item.displayName} has already started`,
      ),
    );
  }
  entry.timers.forEach(clearTimeout);
  watched.delete(operationId);
  emit({
    operationId,
    seq: entry.seq + 1,
    timestampUtc: new Date().toISOString(),
    itemName: entry.item.itemName,
    displayName: entry.item.displayName,
    state: "Canceled",
    progressPercent: 0,
    message: "Canceled by user",
    canceledBy: "user",
    requestedBy: MOCK_USER,
  });
  entry.settle();
  return Promise.resolve();
}

export const api: SofCatApi = {
  listOptionalInstalls: () =>
    offline()
      ? Promise.reject(new Error("mock service unavailable (remove ?offline to reconnect)"))
      : new Promise((resolve) =>
          setTimeout(() => resolve(items.map(({ mock: _mock, ...item }) => item)), 250),
        ),
  installItem: (itemName) => mutate(itemName, false),
  removeItem: (itemName) => mutate(itemName, true),
  watchOperation: (operationId) => watch(operationId),
  cancelOperation: (operationId) => cancel(operationId),
  // `?store=` picks the fixture store; `?unbranded` keeps its items and drops
  // its branding, so tabs fall back to alphabetical order.
  getBranding: () => Promise.resolve({ ...(unbranded() ? UNBRANDED : store.branding) }),
  openExternal(url) {
    window.open(url, "_blank", "noopener,noreferrer");
    return Promise.resolve();
  },
  onOperationStatus(handler) {
    handlers.push(handler);
  },
};

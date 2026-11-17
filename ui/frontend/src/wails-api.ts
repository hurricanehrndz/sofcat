import { Browser, Events } from "@wailsio/runtime";

import { UIService } from "../bindings/github.com/hurricanehrndz/sofcat/ui/index.js";
import type { SofCatApi, OperationStatus } from "./api.ts";

export const api: SofCatApi = {
  listOptionalInstalls: () => UIService.ListOptionalInstalls(),
  installItem: (itemName, placement = "") => UIService.InstallItem(itemName, placement),
  removeItem: (itemName, placement = "") => UIService.RemoveItem(itemName, placement),
  watchOperation: (operationId) => UIService.WatchOperation(operationId),
  cancelOperation: (operationId) => UIService.CancelOperation(operationId),
  getBranding: () => UIService.GetBranding(),
  openExternal: (url) => Browser.OpenURL(url),
  onOperationStatus(handler) {
    Events.On("sofcat:operation-status", (event) => {
      handler(event.data as OperationStatus);
    });
  },
};

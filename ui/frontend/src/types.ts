import type { OptionalInstallItem } from "./api.ts";

// The protocol models stay single-sourced in the generated bindings; only the
// presentation-side shapes live here.
export type { OperationStatus, OptionalInstallItem, OptionalInstallPlacement } from "./api.ts";

export type ActionMethod = "InstallItem" | "RemoveItem";

/**
 * ActionKind is what an action does to the item: do installs it, undo removes
 * it, cancel takes back a pending request.
 */
export type ActionKind = "do" | "undo" | "cancel";

export type ItemAction = {
  kind: ActionKind;
  /** Catalog or placement text, shown only through textContent. */
  label: string;
  method: ActionMethod;
  /** The one placement the action is for; absent for every placement. */
  placement?: string;
};

/** CachedList is the `sofcat.optional-items.v1` localStorage shape. */
export type CachedList = {
  savedAtUtc: string;
  items: OptionalInstallItem[];
};

/**
 * ActivityRecord is one local display-history entry. `state` holds a wire state,
 * a local request state, or one of the local error states from state.ts;
 * `progressPercent` is scoped to `itemName` and is absent on local records.
 */
export type ActivityRecord = {
  operationId: string;
  itemName: string;
  displayName: string;
  state: string;
  message: string;
  timestampUtc: string;
  /** The service's order for the operation's records, from 1; absent on local records. */
  seq?: number;
  progressPercent?: number;
  /** The wire errorMessage on its own, so an outcome line can quote just it. */
  detail?: string;
  /** Who ended a Canceled operation: "user" or "service". */
  canceledBy?: string;
  /** The wire errorCode of a Failed or Deferred record, such as missing_software. */
  errorCode?: string;
};

/** OperationOutcome is where a locally initiated operation has got to. */
export type OperationOutcome = "active" | "terminal" | "error";

/** OperationView is one locally initiated operation and its display timeline. */
export type OperationView = {
  operationId: string;
  item: OptionalInstallItem;
  action: ItemAction;
  records: ActivityRecord[];
  outcome: OperationOutcome;
  /** Set while a CancelOperation request for this operation is in flight. */
  cancelPending?: boolean;
  /** The service's reason for refusing a cancel, shown on the card until the end. */
  notice?: string;
};

/**
 * ActiveOperations maps an accepted operationId to its item, from acceptance
 * until the list fetched after its end arrives.
 */
export type ActiveOperations = ReadonlyMap<string, string>;

/** ListSource is where the currently rendered item list came from. */
export type ListSource = "loading" | "cache" | "live" | "stale";

export type ListView = {
  items: OptionalInstallItem[];
  source: ListSource;
  savedAtUtc: string;
};

/**
 * BrandingView is the shell's organisation branding: admin configuration from
 * the service (policy registry or config.yaml), never catalog data. Empty
 * strings mean "not configured"; brandingView fills in the defaults.
 */
export type BrandingView = {
  title: string;
  tagline: string;
  /** A data: URL for <img>, or "" when there is no usable logo. */
  logoSrc: string;
  /** A data: URL for the banner's background image, or "" to fill it with the accent. */
  backgroundSrc: string;
  /** The banner text colour over the background image; light unless the admin picked dark. */
  backgroundText: "light" | "dark";
  /** The admin's link_url (or help_url), or "" for no link button. */
  helpUrl: string;
  helpLabel: string;
  /** The admin's link_placement: the link button in the banner or at the app bar's end. */
  linkPlacement: "banner" | "app_bar";
  /** The admin's link_position: where in the banner the link sits; auto follows banner_align. */
  linkPosition: "auto" | "top_left" | "top_right" | "bottom_left" | "bottom_right";
  /** #rrggbb, or "" to keep the default accent. */
  accent: string;
  /** Text colour that reads on `accent`. */
  onAccent: string;
  productName: string;
  /** The title's initial, or "" to show the SofCat icon. */
  productMark: string;
  showBanner: boolean;
  /** The Software tab's heading: the admin's list_heading or "Available software". */
  listHeading: string;
  /** The admin's tab order: these sections come first, in this order. */
  sections: string[];
  /** Factors on the default banner title (and page heading) and tagline sizes; 1 keeps them. */
  titleScale: number;
  taglineScale: number;
  /** The admin's content_width: how much of the window the list and pages use. */
  contentWidth: "compact" | "wide" | "full";
  /** The admin's filter_placement: where the list's search and category filters sit. */
  filterPlacement: "right" | "left";
  /**
   * What the app bar shows of the product: mark and name, the mark only, or
   * neither. The name stays for screen readers; full while there is no banner.
   */
  appBarBrand: "full" | "mark" | "none";
  /** The banner's title: text beside a logo tile, or the logo in its place (only with a logo). */
  bannerStyle: "text" | "logo";
  /** The admin's banner_align: where the banner's content sits. */
  bannerAlign: "left" | "center" | "right";
  /** The admin's banner_height: the banner's minimum height, from slim to tall. */
  bannerHeight: "small" | "medium" | "large" | "x-large";
};

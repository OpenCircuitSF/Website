// media.ts — pure decisions for the admin console's image upload control
// (#0433, reopening #0153): the size/format pre-check shown before a round
// trip, the HEIC-specific copy, building the multipart body, and mapping a
// server refusal to the message the editor shows. Kept as a plain,
// unit-testable module with WorkshopEditor.svelte staying thin markup and
// wiring — CLAUDE.md §1's default, and issues/0433.md's plan explicitly
// recommends AGAINST a structural guard here (unlike #0444's autosave
// guard): if the returned path is never assigned into the cover field, the
// field visibly stays empty, so the failure this module could silently let
// through is not actually silent.
//
// Nothing here is the real gate. The server sniffs the format from magic
// bytes only (internal/media.Sniff) and enforces the real size cap
// (admin_media_upload.go's mediaMaxUploadBytes) — this module's checks
// exist purely to give fast, specific feedback before spending a round
// trip, and every one of them is mirrored, never substituted, by a real
// server-side check.

/** Mirrors internal/handlers/admin_media_upload.go's mediaMaxUploadBytes.
 * There is no runtime way to derive one value from the other since they
 * live in different processes (the same limitation admin.ts's
 * importMaxFileBytes documents for the CSV import wizard) — keep them in
 * sync by hand. */
export const MEDIA_MAX_UPLOAD_BYTES = 5 * 1024 * 1024; // 5 MiB

/** File extensions this module treats as HEIC/HEIF for the purpose of the
 * client-side pre-check's specific message. The server's own refusal
 * (internal/media.Sniff) is by magic bytes, not extension — this list only
 * decides which friendly message to show before the round trip; a
 * mislabeled file still gets the server's own accurate 415. */
const HEIC_EXTENSIONS = ['.heic', '.heif'];

/** Extensions the pre-check accepts without a specific warning. A file
 * outside this list is not necessarily rejected by the server (magic bytes
 * decide that) — it only means this pre-check has nothing specific to say
 * and lets the upload proceed to the real check. */
const ACCEPTED_EXTENSIONS = ['.jpg', '.jpeg', '.png'];

/** The reason a client-side pre-check would flag a file before upload. */
export type MediaPrecheckReason = 'too-large' | 'heic' | 'unrecognized-extension';

export interface MediaPrecheckResult {
  ok: boolean;
  reason?: MediaPrecheckReason;
}

/** HEIC_UPLOAD_MESSAGE mirrors internal/handlers/admin_media_upload.go's
 * heicErrorMessage — an iPhone shoots HEIC unless its camera is set to
 * "Most Compatible", so this is the single most likely thing a real
 * admin's first upload hits (issues/0433.md's plan). Named and exported so
 * both this module's own pre-check and a 415 the server names as HEIC can
 * show identical wording. */
export const HEIC_UPLOAD_MESSAGE =
  'This looks like an HEIC/HEIF photo, which this server cannot convert. On an iPhone, use Share > Options and choose JPEG (or set Settings > Camera > Formats > Most Compatible), then upload the exported JPEG.';

/** TOO_LARGE_MESSAGE is computed from MEDIA_MAX_UPLOAD_BYTES so the two
 * never drift apart from each other, even though they can still drift from
 * the Go constant they mirror (see that const's own doc comment). */
export const TOO_LARGE_MESSAGE = `That file is larger than the ${Math.floor(
  MEDIA_MAX_UPLOAD_BYTES / (1024 * 1024),
)} MB upload limit.`;

export const UNRECOGNIZED_EXTENSION_MESSAGE =
  'Only JPEG and PNG images are accepted here — use the path field for anything else already on the server.';

/**
 * precheckFile gives fast, specific feedback before a round trip. It is
 * NEVER the real gate — internal/media.Sniff decides format from magic
 * bytes alone, and admin_media_upload.go enforces the real size cap
 * regardless of what this function says.
 */
export function precheckFile(file: File): MediaPrecheckResult {
  if (file.size > MEDIA_MAX_UPLOAD_BYTES) return { ok: false, reason: 'too-large' };
  const name = file.name.toLowerCase();
  if (HEIC_EXTENSIONS.some((ext) => name.endsWith(ext))) return { ok: false, reason: 'heic' };
  if (!ACCEPTED_EXTENSIONS.some((ext) => name.endsWith(ext))) {
    return { ok: false, reason: 'unrecognized-extension' };
  }
  return { ok: true };
}

/** messageForPrecheckReason maps precheckFile's reason to the copy the
 * editor shows — kept here, not in the component, so it is one thing to
 * test rather than markup to read. */
export function messageForPrecheckReason(reason: MediaPrecheckReason): string {
  switch (reason) {
    case 'too-large':
      return TOO_LARGE_MESSAGE;
    case 'heic':
      return HEIC_UPLOAD_MESSAGE;
    case 'unrecognized-extension':
      return UNRECOGNIZED_EXTENSION_MESSAGE;
  }
}

/** The generic fallback shown when an upload fails with no usable message
 * from the server at all — a network failure, or a non-JSON body. Every
 * refusal admin_media_upload.go actually writes (HEIC, unsupported format,
 * oversize, absurd dimensions, disk space, not configured) carries its own
 * specific `error` string, which messageForUploadError prefers whenever one
 * is present. */
export const GENERIC_UPLOAD_FAILURE_MESSAGE =
  'Upload failed. Please try again, or use the path field with an image already on the server.';

/**
 * messageForUploadError maps whatever POST /admin/media/upload's failure
 * produced (an ApiError from web/src/lib/api.ts, or any other rejection)
 * into the copy the editor shows. The server's own message is used
 * verbatim when present — it already names the specific problem — and this
 * only supplies a fallback for shapes that carry no usable message.
 */
export function messageForUploadError(err: unknown): string {
  if (
    err &&
    typeof err === 'object' &&
    'message' in err &&
    typeof (err as { message?: unknown }).message === 'string' &&
    (err as { message: string }).message.length > 0
  ) {
    return (err as { message: string }).message;
  }
  return GENERIC_UPLOAD_FAILURE_MESSAGE;
}

/** buildMediaUploadFormData is the entire request body POST
 * /admin/media/upload needs: one file under the "file" field, matching
 * internal/handlers/admin_media_upload.go's r.FormFile("file"). Kept here
 * (rather than inlined in web/src/lib/api.ts, mirroring that file's own
 * importFormData) so it is covered by this module's tests. */
export function buildMediaUploadFormData(file: File): FormData {
  const fd = new FormData();
  fd.set('file', file, file.name);
  return fd;
}

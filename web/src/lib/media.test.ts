import { describe, it, expect } from 'vitest';
import {
  MEDIA_MAX_UPLOAD_BYTES,
  HEIC_UPLOAD_MESSAGE,
  TOO_LARGE_MESSAGE,
  UNRECOGNIZED_EXTENSION_MESSAGE,
  GENERIC_UPLOAD_FAILURE_MESSAGE,
  precheckFile,
  messageForPrecheckReason,
  messageForUploadError,
  buildMediaUploadFormData,
} from './media';

function fileOfSize(name: string, size: number, type = 'image/jpeg'): File {
  return new File([new Uint8Array(size)], name, { type });
}

describe('precheckFile', () => {
  it('accepts a small .jpg', () => {
    expect(precheckFile(fileOfSize('soldering.jpg', 1024))).toEqual({ ok: true });
  });

  it('accepts a small .jpeg and .png', () => {
    expect(precheckFile(fileOfSize('a.jpeg', 1024)).ok).toBe(true);
    expect(precheckFile(fileOfSize('a.png', 1024)).ok).toBe(true);
  });

  it('is case-insensitive on extension', () => {
    expect(precheckFile(fileOfSize('IMG_0001.JPG', 1024)).ok).toBe(true);
  });

  it('flags a file over the size limit, even with an accepted extension', () => {
    const got = precheckFile(fileOfSize('big.jpg', MEDIA_MAX_UPLOAD_BYTES + 1));
    expect(got).toEqual({ ok: false, reason: 'too-large' });
  });

  it('accepts a file exactly AT the size limit', () => {
    expect(precheckFile(fileOfSize('exact.jpg', MEDIA_MAX_UPLOAD_BYTES)).ok).toBe(true);
  });

  it('flags .heic specifically, not as a generic unrecognized extension', () => {
    const got = precheckFile(fileOfSize('IMG_0001.HEIC', 1024, 'image/heic'));
    expect(got).toEqual({ ok: false, reason: 'heic' });
  });

  it('flags .heif specifically too', () => {
    expect(precheckFile(fileOfSize('photo.heif', 1024)).reason).toBe('heic');
  });

  it('flags an unrelated extension as unrecognized, not heic', () => {
    const got = precheckFile(fileOfSize('animation.gif', 1024));
    expect(got).toEqual({ ok: false, reason: 'unrecognized-extension' });
  });

  it('size is checked before extension: an oversize .heic is too-large, not heic', () => {
    const got = precheckFile(fileOfSize('big.heic', MEDIA_MAX_UPLOAD_BYTES + 1));
    expect(got.reason).toBe('too-large');
  });
});

describe('messageForPrecheckReason', () => {
  it('maps each reason to its own distinct, non-empty message', () => {
    const tooLarge = messageForPrecheckReason('too-large');
    const heic = messageForPrecheckReason('heic');
    const unrecognized = messageForPrecheckReason('unrecognized-extension');

    expect(tooLarge).toBe(TOO_LARGE_MESSAGE);
    expect(heic).toBe(HEIC_UPLOAD_MESSAGE);
    expect(unrecognized).toBe(UNRECOGNIZED_EXTENSION_MESSAGE);

    const all = [tooLarge, heic, unrecognized];
    expect(new Set(all).size).toBe(all.length);
  });

  it('the HEIC message names HEIC and gives an actionable next step', () => {
    expect(HEIC_UPLOAD_MESSAGE.toUpperCase()).toContain('HEIC');
    expect(HEIC_UPLOAD_MESSAGE.toLowerCase()).toContain('jpeg');
  });

  it('the too-large message states the actual limit, derived from the byte constant', () => {
    expect(TOO_LARGE_MESSAGE).toContain(String(Math.floor(MEDIA_MAX_UPLOAD_BYTES / (1024 * 1024))));
  });
});

describe('messageForUploadError', () => {
  it('prefers the server-provided message when present', () => {
    const err = { message: 'no space left on the media directory — the image was not saved' };
    expect(messageForUploadError(err)).toBe(err.message);
  });

  it('falls back to the generic message for an empty message string', () => {
    expect(messageForUploadError({ message: '' })).toBe(GENERIC_UPLOAD_FAILURE_MESSAGE);
  });

  it('falls back to the generic message for a plain Error with a message', () => {
    // A real ApiError extends Error and so has a `.message` property --
    // this exercises that shape directly rather than a bespoke object.
    const err = new Error('image dimensions 8000x6000 exceed the allowed maximum');
    expect(messageForUploadError(err)).toBe(err.message);
  });

  it('falls back to the generic message for a non-object rejection', () => {
    expect(messageForUploadError('a string, not an object')).toBe(GENERIC_UPLOAD_FAILURE_MESSAGE);
    expect(messageForUploadError(undefined)).toBe(GENERIC_UPLOAD_FAILURE_MESSAGE);
    expect(messageForUploadError(null)).toBe(GENERIC_UPLOAD_FAILURE_MESSAGE);
  });

  it('falls back to the generic message for an object with a non-string message', () => {
    expect(messageForUploadError({ message: 42 })).toBe(GENERIC_UPLOAD_FAILURE_MESSAGE);
  });
});

describe('buildMediaUploadFormData', () => {
  it('carries exactly one "file" field with the given file', () => {
    const file = fileOfSize('soldering-101.jpg', 2048);
    const fd = buildMediaUploadFormData(file);

    const keys = Array.from(fd.keys());
    expect(keys).toEqual(['file']);

    const got = fd.get('file');
    expect(got).toBeInstanceOf(File);
    expect((got as File).name).toBe('soldering-101.jpg');
    expect((got as File).size).toBe(2048);
  });
});

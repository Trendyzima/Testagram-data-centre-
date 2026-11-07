interface R2ObjectBody {
  size: number;
  httpEtag: string;
  body: ReadableStream<Uint8Array>;
  writeHttpMetadata(headers: Headers): void;
}

interface R2Object {
  size: number;
  httpEtag: string;
  writeHttpMetadata(headers: Headers): void;
}

interface R2Bucket {
  head(key: string): Promise<R2Object | null>;
  get(key: string, options?: { range: { offset: number; length: number } }): Promise<R2ObjectBody | null>;
}

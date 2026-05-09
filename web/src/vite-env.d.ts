/// <reference types="vite/client" />

// Declare the env vars we read so TS knows about them.
interface ImportMetaEnv {
  readonly VITE_API_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

/// <reference types="vite/client" />

// Message templates and guide: imported raw, at build time. They live at
// the repository root, where the mass mailing reads them too.
declare module "*?raw" {
  const content: string;
  export default content;
}

interface ImportMetaEnv {
  /** Instance domain campaigns may be pre-filled from; "" disables ?org=. */
  readonly PARAPHE_INSTANCE_DOMAIN: string;
}

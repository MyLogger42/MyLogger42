import { useState } from 'react';
import type { IssuedKey } from '../api/types';

interface Props {
  issued: IssuedKey;
  onClose: () => void;
}

// KeyRevealModal shows the freshly-issued plaintext key value exactly
// once. The component carries the value internally and never persists
// it — closing the modal drops it from memory, and the parent caller
// is expected to discard the IssuedKey reference at the same time.
//
// The single "I've saved it — close" button is intentionally
// non-frictional. We don't gate close on a typed acknowledgement; the
// up-front warning is enough.
export function KeyRevealModal({ issued, onClose }: Props) {
  const [copied, setCopied] = useState(false);

  function copyToClipboard() {
    navigator.clipboard.writeText(issued.value).then(
      () => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1500);
      },
      () => {
        // Clipboard write can fail (insecure context, perms); ignore.
      },
    );
  }

  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true">
      <div className="modal">
        <h2 className="modal-title">New ingest key</h2>
        <p>
          Label: <strong>{issued.label}</strong>
        </p>
        <p className="modal-warn">
          Copy this value now — it won't be shown again.
        </p>
        <pre className="modal-key" data-testid="issued-key">
          {issued.value}
        </pre>
        <div className="modal-actions">
          <button type="button" onClick={copyToClipboard}>
            {copied ? 'Copied' : 'Copy to clipboard'}
          </button>
          <button type="button" onClick={onClose}>
            I've saved it — close
          </button>
        </div>
      </div>
    </div>
  );
}

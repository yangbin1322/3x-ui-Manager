import { describe, it, expect, vi } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';

import BulkAddServersModal from '@/pages/nodes/BulkAddServersModal';
import { renderWithProviders } from './test-utils';

function buttonByText(re: RegExp): HTMLElement {
  const btn = Array.from(document.querySelectorAll('button')).find((b) => re.test(b.textContent ?? ''));
  if (!btn) throw new Error(`button matching ${re} not found`);
  return btn as HTMLElement;
}

describe('BulkAddServersModal (paste import)', () => {
  it('parses pasted CSV into a preview, then imports only valid rows', async () => {
    const createBatch = vi.fn().mockResolvedValue({
      success: true,
      obj: { results: [{ index: 0, success: true, name: 'hk-1' }] },
    });

    renderWithProviders(
      <BulkAddServersModal open createBatch={createBatch} onOpenChange={() => {}} />,
    );

    // Paste a header + one valid row + one row with no credential (skipped).
    const textarea = document.querySelector('textarea') as HTMLTextAreaElement;
    fireEvent.change(textarea, {
      target: {
        value: 'name,address,sshPort,sshUser,sshPassword,sshPrivateKey,sshHostKeyMode\n'
          + 'hk-1,203.0.113.5,22,root,secret,,trust\n'
          + 'bad,203.0.113.6,22,root,,,trust',
      },
    });
    fireEvent.click(buttonByText(/parse/i));

    // Preview shows both parsed rows.
    await waitFor(() => {
      const body = document.body.textContent ?? '';
      expect(body).toContain('203.0.113.5');
      expect(body).toContain('203.0.113.6');
    });

    fireEvent.click(buttonByText(/import/i));
    await waitFor(() => expect(createBatch).toHaveBeenCalledTimes(1));

    // Only the row with a credential is submitted, and verify defaults to true.
    const [payload, verify] = createBatch.mock.calls[0];
    expect(payload).toHaveLength(1);
    expect(payload[0]).toMatchObject({ name: 'hk-1', address: '203.0.113.5', sshAuthType: 'password', sshPassword: 'secret' });
    expect(verify).toBe(true);
  });

  it('keeps the import button disabled until rows are parsed', () => {
    renderWithProviders(
      <BulkAddServersModal open createBatch={vi.fn()} onOpenChange={() => {}} />,
    );
    expect(buttonByText(/import/i).hasAttribute('disabled')).toBe(true);
  });
});

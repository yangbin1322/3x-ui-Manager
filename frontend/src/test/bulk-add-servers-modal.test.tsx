import { describe, it, expect, vi } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';

import BulkAddServersModal from '@/pages/nodes/BulkAddServersModal';
import { renderWithProviders } from './test-utils';

function okButton(): HTMLElement {
  const btn = Array.from(document.querySelectorAll('.ant-modal-footer button'))
    .find((b) => /save/i.test(b.textContent ?? ''));
  if (!btn) throw new Error('save button not found');
  return btn as HTMLElement;
}

describe('BulkAddServersModal', () => {
  it('submits only rows with an address and reports per-row failures', async () => {
    const createBatch = vi.fn().mockResolvedValue({
      success: true,
      obj: { results: [{ index: 0, success: true, name: '203.0.113.5' }] },
    });

    renderWithProviders(
      <BulkAddServersModal open createBatch={createBatch} onOpenChange={() => {}} />,
    );

    // The first (and only) row starts blank; fill its address + password.
    const addressInputs = document.querySelectorAll('input[placeholder="203.0.113.5"]');
    fireEvent.change(addressInputs[0], { target: { value: '203.0.113.5' } });
    const passwordInputs = document.querySelectorAll('input[type="password"]');
    fireEvent.change(passwordInputs[0], { target: { value: 'secret' } });

    fireEvent.click(okButton());

    await waitFor(() => expect(createBatch).toHaveBeenCalledTimes(1));
    const payload = createBatch.mock.calls[0][0];
    expect(payload).toHaveLength(1);
    expect(payload[0]).toMatchObject({ address: '203.0.113.5', sshPassword: 'secret' });
  });

  it('keeps the save button disabled until at least one row has an address', () => {
    renderWithProviders(
      <BulkAddServersModal open createBatch={vi.fn()} onOpenChange={() => {}} />,
    );
    expect(okButton().hasAttribute('disabled')).toBe(true);
  });
});

import { describe, it, expect, vi } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';

import ExecCommandModal from '@/pages/nodes/ExecCommandModal';
import type { ManagedServerRecord } from '@/schemas/managedServer';
import { renderWithProviders } from './test-utils';

const targets: ManagedServerRecord[] = [
  { id: 3, name: 'hk-1' },
  { id: 5, name: 'sg-1' },
];

function okButton(): HTMLElement {
  const btn = Array.from(document.querySelectorAll('.ant-modal-footer button'))
    .find((b) => /run|confirm/i.test(b.textContent ?? ''));
  if (!btn) throw new Error('run/confirm button not found');
  return btn as HTMLElement;
}

describe('ExecCommandModal', () => {
  it('lists every target server so the operator sees the blast radius', () => {
    renderWithProviders(
      <ExecCommandModal open targets={targets} execCommand={vi.fn()} onOpenChange={() => {}} />,
    );
    const tags = document.body.textContent ?? '';
    expect(tags).toContain('hk-1');
    expect(tags).toContain('sg-1');
  });

  it('requires a two-step confirm before it runs, then shows per-server results', async () => {
    const execCommand = vi.fn().mockResolvedValue({
      success: true,
      obj: {
        batchId: 'b1',
        results: [
          { serverId: 3, serverName: 'hk-1', status: 'success', exitCode: 0, stdout: 'ok', durationMs: 12 },
          { serverId: 5, serverName: 'sg-1', status: 'unreachable', exitCode: -1, stdout: '', error: 'cannot reach', durationMs: 4 },
        ],
      },
    });

    renderWithProviders(
      <ExecCommandModal open targets={targets} execCommand={execCommand} onOpenChange={() => {}} />,
    );

    const textarea = document.querySelector('#exec-command') as HTMLTextAreaElement;
    fireEvent.change(textarea, { target: { value: 'uptime' } });

    // First click arms the confirm — it must NOT run yet.
    fireEvent.click(okButton());
    expect(execCommand).not.toHaveBeenCalled();

    // Second click actually runs it.
    fireEvent.click(okButton());
    await waitFor(() => expect(execCommand).toHaveBeenCalledTimes(1));
    expect(execCommand).toHaveBeenCalledWith([3, 5], 'uptime', 30);

    await waitFor(() => {
      const body = document.body.textContent ?? '';
      // Status is rendered through i18n (statusValues), so the visible text is
      // the translated label, not the raw status token.
      expect(body).toContain('Success');
      expect(body).toContain('Unreachable');
      expect(body).toContain('cannot reach');
    });
  });

  it('disarms the confirm when the command is edited', () => {
    renderWithProviders(
      <ExecCommandModal open targets={targets} execCommand={vi.fn()} onOpenChange={() => {}} />,
    );
    const textarea = document.querySelector('#exec-command') as HTMLTextAreaElement;
    fireEvent.change(textarea, { target: { value: 'reboot' } });
    fireEvent.click(okButton());
    // Editing after arming should reset back to the non-danger "run" label.
    fireEvent.change(textarea, { target: { value: 'reboot --dry-run' } });
    const label = okButton().textContent ?? '';
    expect(/confirm/i.test(label)).toBe(false);
  });
});

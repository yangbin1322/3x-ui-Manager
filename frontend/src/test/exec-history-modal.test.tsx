import { describe, it, expect, vi } from 'vitest';
import { waitFor } from '@testing-library/react';

import ExecHistoryModal from '@/pages/nodes/ExecHistoryModal';
import { renderWithProviders } from './test-utils';

describe('ExecHistoryModal', () => {
  it('loads the first page and renders audit rows with translated status', async () => {
    const fetchHistory = vi.fn().mockResolvedValue({
      success: true,
      obj: {
        items: [
          { id: 2, serverId: 1, serverName: 'hk-1', username: 'admin', command: 'uptime', status: 'success', exitCode: 0, stdout: '10:00 up', durationMs: 12, createdAt: 1700000000000 },
          { id: 1, serverId: 2, serverName: 'sg-1', username: 'ops', command: 'reboot', status: 'unreachable', exitCode: -1, stdout: '', error: 'cannot reach', durationMs: 4, createdAt: 1699999999000 },
        ],
        total: 2,
        page: 1,
        pageSize: 20,
      },
    });

    renderWithProviders(
      <ExecHistoryModal open fetchHistory={fetchHistory} onOpenChange={() => {}} />,
    );

    await waitFor(() => expect(fetchHistory).toHaveBeenCalledWith({ page: 1, pageSize: 20 }));
    await waitFor(() => {
      const body = document.body.textContent ?? '';
      expect(body).toContain('uptime');
      expect(body).toContain('reboot');
      expect(body).toContain('admin');
      // Status is translated through pages.nodes.exec.status.*
      expect(body).toContain('Success');
      expect(body).toContain('Unreachable');
    });
  });

  it('does not fetch while closed', () => {
    const fetchHistory = vi.fn();
    renderWithProviders(
      <ExecHistoryModal open={false} fetchHistory={fetchHistory} onOpenChange={() => {}} />,
    );
    expect(fetchHistory).not.toHaveBeenCalled();
  });
});

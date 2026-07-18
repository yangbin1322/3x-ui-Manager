import { describe, it, expect, vi, afterEach } from 'vitest';
import { fireEvent } from '@testing-library/react';

import ServerList from '@/pages/nodes/ServerList';
import type { ManagedServerRecord } from '@/schemas/managedServer';

import { renderWithProviders } from './test-utils';

const noop = () => {};

function servers(): ManagedServerRecord[] {
  return [
    { id: 1, name: 'bare-box', enable: true, status: 'reachable', nodeId: 0 },
    { id: 2, name: 'installed-box', enable: true, status: 'reachable', nodeId: 7 },
  ];
}

function renderList(overrides: Partial<Parameters<typeof ServerList>[0]> = {}) {
  renderWithProviders(
    <ServerList
      servers={servers()}
      nodeNameById={new Map([[7, 'hk-panel']])}
      selectedIds={[]}
      onSelectionChange={noop}
      onAdd={noop}
      onEdit={noop}
      onDelete={noop}
      onToggleEnable={noop}
      onInstall={noop}
      onViewNode={noop}
      onExecSelected={noop}
      onExecHistory={noop}
      {...overrides}
    />,
  );
}

describe('ServerList install action', () => {
  afterEach(() => vi.restoreAllMocks());

  it('shows the install action only for servers without a linked node', () => {
    const onInstall = vi.fn();
    renderList({ onInstall });

    const installButtons = Array.from(
      document.querySelectorAll('button[aria-label="Install 3x-ui"]'),
    );
    // Only the server without a derived node offers the install.
    expect(installButtons.length).toBe(1);

    fireEvent.click(installButtons[0]);
    expect(onInstall).toHaveBeenCalledTimes(1);
    expect(onInstall.mock.calls[0][0]).toMatchObject({ id: 1 });
  });

  it('links a derived server to its panel node by name', () => {
    renderList();
    const body = document.body.textContent ?? '';
    expect(body).toContain('hk-panel');
  });
});

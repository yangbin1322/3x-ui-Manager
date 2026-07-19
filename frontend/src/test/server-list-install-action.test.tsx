import { describe, it, expect, vi, afterEach } from 'vitest';
import { fireEvent } from '@testing-library/react';

import ServerList from '@/pages/nodes/ServerList';
import type { ManagedServerRecord } from '@/schemas/managedServer';

import { renderWithProviders } from './test-utils';

const noop = () => {};

// Three panel states drive the row actions: a bare box offers Install, a box
// that already runs a panel but is not yet a node offers Import, and either an
// installed or linked box offers Uninstall.
function servers(): ManagedServerRecord[] {
  return [
    { id: 1, name: 'bare-box', enable: true, status: 'reachable', nodeId: 0, panelInstalled: false },
    { id: 2, name: 'installed-box', enable: true, status: 'reachable', nodeId: 0, panelInstalled: true, panelVersion: 'v2.6.0' },
    { id: 3, name: 'linked-box', enable: true, status: 'reachable', nodeId: 7, panelInstalled: true },
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
      onImport={noop}
      onUninstall={noop}
      onViewNode={noop}
      onExecSelected={noop}
      onBatchInstall={noop}
      onBatchImport={noop}
      onBatchUninstall={noop}
      onExecHistory={noop}
      {...overrides}
    />,
  );
}

describe('ServerList row actions', () => {
  afterEach(() => vi.restoreAllMocks());

  it('offers Install only for a bare box', () => {
    const onInstall = vi.fn();
    renderList({ onInstall });
    const buttons = Array.from(document.querySelectorAll('button[aria-label="Install 3x-ui"]'));
    expect(buttons.length).toBe(1);
    fireEvent.click(buttons[0]);
    expect(onInstall.mock.calls[0][0]).toMatchObject({ id: 1 });
  });

  it('offers Import only for an installed but unlinked box', () => {
    const onImport = vi.fn();
    renderList({ onImport });
    const buttons = Array.from(document.querySelectorAll('button[aria-label="Import as node"]'));
    expect(buttons.length).toBe(1);
    fireEvent.click(buttons[0]);
    expect(onImport.mock.calls[0][0]).toMatchObject({ id: 2 });
  });

  it('offers Uninstall for installed and linked boxes', () => {
    renderList();
    const buttons = Array.from(document.querySelectorAll('button[aria-label="Uninstall 3x-ui"]'));
    // installed-box and linked-box, not the bare box.
    expect(buttons.length).toBe(2);
  });

  it('shows the panel version and links a derived server to its node', () => {
    renderList();
    const body = document.body.textContent ?? '';
    expect(body).toContain('v2.6.0');
    expect(body).toContain('hk-panel');
  });
});

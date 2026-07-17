import { describe, it, expect, vi, afterEach } from 'vitest';
import { fireEvent } from '@testing-library/react';

import NodeList from '@/pages/nodes/NodeList';
import type { NodeRecord } from '@/schemas/node';

import { renderWithProviders } from './test-utils';

const noop = () => {};

function nodes(): NodeRecord[] {
  return [
    { id: 1, name: 'ssh-box', mode: 'ssh', enable: true, status: 'reachable' },
    { id: 2, name: 'api-box', mode: 'api', enable: true, status: 'online' },
  ];
}

describe('NodeList install action', () => {
  afterEach(() => vi.restoreAllMocks());

  it('shows the install action only for ssh nodes and invokes it with that node', () => {
    const onInstall = vi.fn();
    renderWithProviders(
      <NodeList
        nodes={nodes()}
        isMobile={false}
        selectedIds={[]}
        onSelectionChange={noop}
        onAdd={noop}
        onMtls={noop}
        onEdit={noop}
        onDelete={noop}
        onProbe={noop}
        onToggleEnable={noop}
        onUpdateNode={noop}
        onInstall={onInstall}
        onUpdateSelected={noop}
        onExecSelected={noop}
        onExecHistory={noop}
      />,
    );

    const installButtons = Array.from(
      document.querySelectorAll('button[aria-label="Install 3x-ui"]'),
    );
    // Exactly one ssh node → exactly one install button.
    expect(installButtons.length).toBe(1);

    fireEvent.click(installButtons[0]);
    expect(onInstall).toHaveBeenCalledTimes(1);
    expect(onInstall.mock.calls[0][0]).toMatchObject({ id: 1, mode: 'ssh' });
  });
});

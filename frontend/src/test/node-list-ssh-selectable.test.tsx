import { describe, it, expect, vi, afterEach } from 'vitest';

import NodeList from '@/pages/nodes/NodeList';
import type { NodeRecord } from '@/schemas/node';

import { renderWithProviders } from './test-utils';

const noop = () => {};

// An enabled ssh node sits at status "reachable", never "online", so the
// checkbox must not be gated on the update-eligibility (online) check — it is
// selectable for command execution. A second node makes the selection column
// render (it only appears when more than one row exists).
function nodes(): NodeRecord[] {
  return [
    { id: 1, name: 'ssh-box', mode: 'ssh', enable: true, status: 'reachable' },
    { id: 2, name: 'api-box', mode: 'api', enable: true, status: 'online' },
  ];
}

function renderList() {
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
      onInstall={noop}
      onUpdateSelected={noop}
      onExecSelected={noop}
      onExecHistory={noop}
    />,
  );
}

describe('NodeList ssh-node selection', () => {
  afterEach(() => vi.restoreAllMocks());

  it('leaves the enabled ssh node checkbox enabled despite the reachable status', () => {
    renderList();
    const rowCheckboxes = Array.from(
      document.querySelectorAll('tbody .ant-checkbox-input'),
    ) as HTMLInputElement[];
    expect(rowCheckboxes.length).toBeGreaterThan(0);
    // No selectable row checkbox should be disabled — the ssh node in particular.
    expect(rowCheckboxes.some((c) => !c.disabled)).toBe(true);
  });
});

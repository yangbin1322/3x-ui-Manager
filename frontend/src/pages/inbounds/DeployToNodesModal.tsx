import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Checkbox, Modal, Tag, Tooltip, Empty } from 'antd';
import type { NodeRecord } from '@/api/queries/useNodesQuery';
import type { DeployResponse } from '@/generated/types';
import { HttpUtil } from '@/utils';

interface DeployTarget {
  id: number;
  tag: string;
  nodeId?: number | null;
}

interface DeployToNodesModalProps {
  open: boolean;
  inbound: DeployTarget | null;
  nodes: NodeRecord[];
  onOpenChange: (open: boolean) => void;
  onDeployed?: () => void;
}

export default function DeployToNodesModal({ open, inbound, nodes, onOpenChange, onDeployed }: DeployToNodesModalProps) {
  const { t } = useTranslation();
  const [selected, setSelected] = useState<number[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [results, setResults] = useState<DeployResponse['results'] | null>(null);

  // Eligible targets: enabled nodes other than the one the source inbound
  // already lives on (a copy there would collide with itself).
  const targets = useMemo(
    () => nodes.filter((n) => n.enable && n.id !== (inbound?.nodeId ?? -1)),
    [nodes, inbound],
  );

  useEffect(() => {
    if (open) {
      setSelected([]);
      setResults(null);
      setSubmitting(false);
    }
  }, [open]);

  const canDeploy = selected.length > 0 && !submitting && !!inbound;

  async function deploy() {
    if (!inbound) return;
    setSubmitting(true);
    setResults(null);
    try {
      const msg = await HttpUtil.post<DeployResponse>('/panel/api/inbounds/deployToNodes',
        { inboundId: inbound.id, nodeIds: selected },
        { headers: { 'Content-Type': 'application/json' } });
      const rows = msg?.obj?.results ?? [];
      setResults(rows);
      if (rows.length > 0 && rows.every((r) => r.success)) {
        onDeployed?.();
        onOpenChange(false);
      } else {
        onDeployed?.();
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Modal
      open={open}
      title={t('pages.inbounds.deployToNodes')}
      okText={t('pages.inbounds.deployToNodesConfirm', { count: selected.length })}
      cancelText={results ? t('close') : t('cancel')}
      okButtonProps={{ disabled: !canDeploy, loading: submitting }}
      onOk={deploy}
      onCancel={() => { if (!submitting) onOpenChange(false); }}
      maskClosable={false}
    >
      <Alert type="info" showIcon style={{ marginBottom: 12 }} message={t('pages.inbounds.deployToNodesHint')} />

      {inbound && (
        <div style={{ marginBottom: 12 }}>
          <span style={{ opacity: 0.7 }}>{t('pages.inbounds.deploySource')}: </span>
          <Tag color="processing">{inbound.tag}</Tag>
        </div>
      )}

      {targets.length === 0 ? (
        <Empty description={t('pages.inbounds.deployNoNodes')} />
      ) : (
        <Checkbox.Group
          value={selected}
          onChange={(v) => setSelected(v as number[])}
          style={{ display: 'flex', flexDirection: 'column', gap: 8 }}
        >
          {targets.map((n) => {
            const result = results?.find((r) => r.nodeId === n.id);
            return (
              <div key={n.id} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Checkbox value={n.id} disabled={submitting}>
                  {n.name || `#${n.id}`}
                  <span style={{ opacity: 0.5, marginInlineStart: 6 }}>{n.address}</span>
                </Checkbox>
                {result && (result.success
                  ? <Tag color="success">{result.tag}</Tag>
                  : <Tooltip title={result.message}><Tag color="error">!</Tag></Tooltip>)}
              </div>
            );
          })}
        </Checkbox.Group>
      )}
    </Modal>
  );
}

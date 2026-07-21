import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Checkbox, Modal, Segmented, Select, Tag, Tooltip, Empty } from 'antd';
import type { NodeRecord } from '@/api/queries/useNodesQuery';
import type { DeployResponse } from '@/generated/types';
import { HttpUtil } from '@/utils';

type ClientMode = 'none' | 'copy' | 'bind';

interface RawClient {
  email?: string;
}

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
  const [clientMode, setClientMode] = useState<ClientMode>('none');
  const [bindEmails, setBindEmails] = useState<string[]>([]);
  const [clientEmails, setClientEmails] = useState<string[]>([]);
  const [clientsLoading, setClientsLoading] = useState(false);

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
      setClientMode('none');
      setBindEmails([]);
    }
  }, [open]);

  useEffect(() => {
    if (!open || clientMode !== 'bind' || clientEmails.length > 0 || clientsLoading) return;
    let cancelled = false;
    setClientsLoading(true);
    HttpUtil.get('/panel/api/clients/list', undefined, { silent: true })
      .then((msg) => {
        if (cancelled) return;
        const list = Array.isArray(msg?.obj) ? (msg.obj as RawClient[]) : [];
        const emails = list.map((c) => (c?.email || '').trim()).filter(Boolean);
        setClientEmails([...new Set(emails)].sort((a, b) => a.localeCompare(b)));
      })
      .finally(() => {
        if (!cancelled) setClientsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, clientMode, clientEmails.length, clientsLoading]);

  const bindMissing = clientMode === 'bind' && bindEmails.length === 0;
  const canDeploy = selected.length > 0 && !submitting && !!inbound && !bindMissing;

  async function deploy() {
    if (!inbound) return;
    setSubmitting(true);
    setResults(null);
    try {
      const msg = await HttpUtil.post<DeployResponse>('/panel/api/inbounds/deployToNodes',
        {
          inboundId: inbound.id,
          nodeIds: selected,
          clientMode,
          clientEmails: clientMode === 'bind' ? bindEmails : [],
        },
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

      <div style={{ marginBottom: 12 }}>
        <div style={{ marginBottom: 6, opacity: 0.7 }}>{t('pages.inbounds.deployClients')}</div>
        <Segmented<ClientMode>
          value={clientMode}
          onChange={setClientMode}
          disabled={submitting}
          options={[
            { value: 'none', label: t('pages.inbounds.deployClientsNone') },
            { value: 'copy', label: t('pages.inbounds.deployClientsCopy') },
            { value: 'bind', label: t('pages.inbounds.deployClientsBind') },
          ]}
        />
        {clientMode === 'bind' && (
          <Select
            mode="multiple"
            style={{ width: '100%', marginTop: 8 }}
            value={bindEmails}
            onChange={setBindEmails}
            loading={clientsLoading}
            disabled={submitting}
            options={clientEmails.map((e) => ({ value: e, label: e }))}
            placeholder={t('pages.inbounds.deployClientsBindPlaceholder')}
            status={bindMissing ? 'error' : undefined}
            showSearch={{ optionFilterProp: 'label' }}
          />
        )}
        <div style={{ opacity: 0.55, marginTop: 6, fontSize: 12 }}>
          {t(`pages.inbounds.deployClientsHint.${clientMode}`)}
        </div>
      </div>

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
                  ? <Tag color="success">{result.tag}{result.attached ? ` +${result.attached}` : ''}</Tag>
                  : <Tooltip title={result.message}><Tag color="error">!</Tag></Tooltip>)}
              </div>
            );
          })}
        </Checkbox.Group>
      )}
    </Modal>
  );
}

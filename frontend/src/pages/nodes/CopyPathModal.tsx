import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Input, InputNumber, Modal, Select, Tag, Tooltip } from 'antd';
import type { ManagedServerRecord } from '@/schemas/managedServer';
import type { BatchCopyResult, CopyResult } from '@/generated/types';
import type { Msg } from '@/utils';
import './ExecCommandModal.css';

interface CopyPathModalProps {
  open: boolean;
  targets: ManagedServerRecord[];
  allServers: ManagedServerRecord[];
  copyPath: (sourceId: number, sourcePath: string, targetIds: number[], dest: string, timeoutSec: number) => Promise<Msg<BatchCopyResult>>;
  onOpenChange: (open: boolean) => void;
}

function statusColor(status: string): string {
  switch (status) {
    case 'success': return 'success';
    case 'unreachable': return 'default';
    case 'timeout': return 'warning';
    default: return 'error';
  }
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

export default function CopyPathModal({ open, targets, allServers, copyPath, onOpenChange }: CopyPathModalProps) {
  const { t } = useTranslation();
  const [sourceId, setSourceId] = useState<number | undefined>(undefined);
  const [sourcePath, setSourcePath] = useState('');
  const [dest, setDest] = useState('');
  const [timeoutSec, setTimeoutSec] = useState(300);
  const [running, setRunning] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [results, setResults] = useState<CopyResult[] | null>(null);

  useEffect(() => {
    if (open) {
      setSourceId(undefined);
      setSourcePath('');
      setDest('');
      setTimeoutSec(300);
      setResults(null);
      setConfirming(false);
      setRunning(false);
    }
  }, [open]);

  const targetIds = useMemo(() => targets.map((s) => s.id), [targets]);
  const sourceOptions = useMemo(
    () => allServers.map((s) => ({ value: s.id, label: s.name || `#${s.id}` })),
    [allServers],
  );
  const canRun = sourceId != null && sourcePath.trim().length > 0 && dest.trim().length > 0 && targetIds.length > 0 && !running;

  async function doRun() {
    if (sourceId == null) return;
    setRunning(true);
    setResults(null);
    try {
      const msg = await copyPath(sourceId, sourcePath.trim(), targetIds, dest.trim(), timeoutSec);
      setResults(msg?.success && msg.obj ? (msg.obj.results ?? []) : []);
    } finally {
      setRunning(false);
      setConfirming(false);
    }
  }

  const summary = useMemo(() => {
    if (!results) return null;
    const ok = results.filter((r) => r.status === 'success').length;
    return { ok, failed: results.length - ok };
  }, [results]);

  function close() {
    if (!running) onOpenChange(false);
  }

  return (
    <Modal
      open={open}
      title={t('pages.nodes.copy.title')}
      width="720px"
      okText={confirming ? t('pages.nodes.copy.confirmCopy') : t('pages.nodes.copy.copy')}
      okButtonProps={{ danger: confirming, disabled: !canRun, loading: running }}
      cancelText={confirming ? t('cancel') : t('close')}
      onOk={() => (confirming ? doRun() : setConfirming(true))}
      onCancel={() => (confirming ? setConfirming(false) : close())}
      maskClosable={false}
    >
      <label className="exec-label" htmlFor="copy-source">{t('pages.nodes.copy.source')}</label>
      <Select
        id="copy-source"
        value={sourceId}
        onChange={(v) => { setSourceId(v); setConfirming(false); }}
        options={sourceOptions}
        showSearch
        optionFilterProp="label"
        placeholder={t('pages.nodes.copy.source')}
        style={{ width: '100%' }}
        disabled={running}
      />

      <label className="exec-label" htmlFor="copy-source-path" style={{ marginTop: 12 }}>{t('pages.nodes.copy.sourcePath')}</label>
      <Tooltip title={t('pages.nodes.copy.sourcePathHint')}>
        <Input
          id="copy-source-path"
          value={sourcePath}
          onChange={(e) => { setSourcePath(e.target.value); setConfirming(false); }}
          placeholder={t('pages.nodes.copy.sourcePathPlaceholder')}
          disabled={running}
          spellCheck={false}
        />
      </Tooltip>

      <div className="exec-targets" style={{ marginTop: 12 }}>
        <span className="exec-label">{t('pages.nodes.copy.targets', { count: targets.length })}</span>
        <div className="exec-target-tags">
          {targets.map((s) => (
            <Tag key={s.id} color="processing">{s.name || `#${s.id}`}</Tag>
          ))}
        </div>
      </div>

      <label className="exec-label" htmlFor="copy-dest">{t('pages.nodes.copy.dest')}</label>
      <Tooltip title={t('pages.nodes.copy.destHint')}>
        <Input
          id="copy-dest"
          value={dest}
          onChange={(e) => { setDest(e.target.value); setConfirming(false); }}
          placeholder={t('pages.nodes.copy.destPlaceholder')}
          disabled={running}
          spellCheck={false}
        />
      </Tooltip>

      <div className="exec-timeout">
        <span className="exec-label">{t('pages.nodes.copy.timeout')}</span>
        <Tooltip title={t('pages.nodes.copy.timeoutHint')}>
          <InputNumber
            min={1}
            max={3600}
            value={timeoutSec}
            onChange={(v) => setTimeoutSec(typeof v === 'number' ? v : 300)}
            addonAfter="s"
            disabled={running}
          />
        </Tooltip>
      </div>

      {confirming && !results && (
        <Alert
          type="warning"
          showIcon
          style={{ marginTop: 12 }}
          message={t('pages.nodes.copy.confirmTitle', { count: targets.length })}
          description={t('pages.nodes.copy.confirmBody')}
        />
      )}

      {results && (
        <div className="exec-results">
          {summary && (
            <div className="exec-summary">
              <Tag color="success">{t('pages.nodes.copy.okCount', { count: summary.ok })}</Tag>
              {summary.failed > 0 && <Tag color="error">{t('pages.nodes.copy.failedCount', { count: summary.failed })}</Tag>}
            </div>
          )}
          {results.map((r) => (
            <div key={r.serverId} className="exec-result-row">
              <div className="exec-result-head">
                <span className="exec-result-node">{r.serverName || `#${r.serverId}`}</span>
                <Tag color={statusColor(r.status)}>{t(`pages.nodes.copy.status.${r.status}`)}</Tag>
                {r.status === 'success' && (
                  <span className="exec-result-dur">{t('pages.nodes.copy.filesCount', { count: r.files })} · {formatBytes(r.bytes)} → {r.path}</span>
                )}
                <span className="exec-result-dur">{r.durationMs} ms</span>
              </div>
              {r.error && <pre className="exec-result-out">{r.error}</pre>}
            </div>
          ))}
        </div>
      )}
    </Modal>
  );
}

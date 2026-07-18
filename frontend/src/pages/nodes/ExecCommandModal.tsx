import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Input, InputNumber, Modal, Tag, Tooltip } from 'antd';
import type { ManagedServerRecord } from '@/schemas/managedServer';
import type { BatchExecResult, ExecResult } from '@/generated/types';
import type { Msg } from '@/utils';
import './ExecCommandModal.css';

interface ExecCommandModalProps {
  open: boolean;
  targets: ManagedServerRecord[];
  execCommand: (serverIds: number[], command: string, timeoutSec: number) => Promise<Msg<BatchExecResult>>;
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

export default function ExecCommandModal({ open, targets, execCommand, onOpenChange }: ExecCommandModalProps) {
  const { t } = useTranslation();
  const [command, setCommand] = useState('');
  const [timeoutSec, setTimeoutSec] = useState(30);
  const [running, setRunning] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [results, setResults] = useState<ExecResult[] | null>(null);

  useEffect(() => {
    if (open) {
      setCommand('');
      setTimeoutSec(30);
      setResults(null);
      setConfirming(false);
      setRunning(false);
    }
  }, [open]);

  const serverIds = useMemo(() => targets.map((s) => s.id), [targets]);
  const canRun = command.trim().length > 0 && serverIds.length > 0 && !running;

  async function doRun() {
    setRunning(true);
    setResults(null);
    try {
      const msg = await execCommand(serverIds, command.trim(), timeoutSec);
      if (msg?.success && msg.obj) {
        setResults(msg.obj.results ?? []);
      } else {
        setResults([]);
      }
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
      title={t('pages.nodes.exec.title')}
      width="720px"
      okText={confirming ? t('pages.nodes.exec.confirmRun') : t('pages.nodes.exec.run')}
      okButtonProps={{ danger: confirming, disabled: !canRun, loading: running }}
      cancelText={confirming ? t('cancel') : t('close')}
      onOk={() => (confirming ? doRun() : setConfirming(true))}
      onCancel={() => (confirming ? setConfirming(false) : close())}
      maskClosable={false}
    >
      <div className="exec-targets">
        <span className="exec-label">{t('pages.nodes.exec.targets', { count: targets.length })}</span>
        <div className="exec-target-tags">
          {targets.map((s) => (
            <Tag key={s.id} color="processing">{s.name || `#${s.id}`}</Tag>
          ))}
        </div>
      </div>

      <label className="exec-label" htmlFor="exec-command">{t('pages.nodes.exec.command')}</label>
      <Input.TextArea
        id="exec-command"
        value={command}
        onChange={(e) => { setCommand(e.target.value); setConfirming(false); }}
        placeholder="apt-get install -y curl"
        autoSize={{ minRows: 2, maxRows: 6 }}
        disabled={running}
        spellCheck={false}
      />

      <div className="exec-timeout">
        <span className="exec-label">{t('pages.nodes.exec.timeout')}</span>
        <Tooltip title={t('pages.nodes.exec.timeoutHint')}>
          <InputNumber
            min={1}
            max={300}
            value={timeoutSec}
            onChange={(v) => setTimeoutSec(typeof v === 'number' ? v : 30)}
            addonAfter="s"
            disabled={running}
          />
        </Tooltip>
      </div>

      <Alert
        type="info"
        showIcon
        style={{ marginTop: 12 }}
        message={t('pages.nodes.exec.nonInteractiveHint')}
      />

      {confirming && !results && (
        <Alert
          type="warning"
          showIcon
          style={{ marginTop: 12 }}
          message={t('pages.nodes.exec.confirmTitle', { count: targets.length })}
          description={t('pages.nodes.exec.confirmBody')}
        />
      )}

      {results && (
        <div className="exec-results">
          {summary && (
            <div className="exec-summary">
              <Tag color="success">{t('pages.nodes.exec.okCount', { count: summary.ok })}</Tag>
              {summary.failed > 0 && <Tag color="error">{t('pages.nodes.exec.failedCount', { count: summary.failed })}</Tag>}
            </div>
          )}
          {results.map((r) => (
            <div key={r.serverId} className="exec-result-row">
              <div className="exec-result-head">
                <span className="exec-result-node">{r.serverName || `#${r.serverId}`}</span>
                <Tag color={statusColor(r.status)}>{t(`pages.nodes.exec.status.${r.status}`)}</Tag>
                <span className="exec-result-exit">exit {r.exitCode}</span>
                <span className="exec-result-dur">{r.durationMs} ms</span>
              </div>
              {(r.stdout || r.error) && (
                <pre className="exec-result-out">{r.error ? `${r.error}\n${r.stdout}` : r.stdout}</pre>
              )}
            </div>
          ))}
        </div>
      )}
    </Modal>
  );
}

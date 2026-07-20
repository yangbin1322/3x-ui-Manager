import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Input, InputNumber, Modal, Segmented, Tag, Tooltip, Upload } from 'antd';
import { UploadOutlined } from '@ant-design/icons';
import type { UploadFile } from 'antd';
import type { ManagedServerRecord } from '@/schemas/managedServer';
import type { BatchUploadResult, UploadResult } from '@/generated/types';
import type { Msg } from '@/utils';
import './ExecCommandModal.css';

interface UploadFileModalProps {
  open: boolean;
  targets: ManagedServerRecord[];
  uploadFile: (serverIds: number[], files: File[], dest: string, timeoutSec: number) => Promise<Msg<BatchUploadResult>>;
  onOpenChange: (open: boolean) => void;
}

type Mode = 'files' | 'directory';

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

export default function UploadFileModal({ open, targets, uploadFile, onOpenChange }: UploadFileModalProps) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<Mode>('files');
  const [fileList, setFileList] = useState<UploadFile[]>([]);
  const [dest, setDest] = useState('');
  const [timeoutSec, setTimeoutSec] = useState(120);
  const [running, setRunning] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [results, setResults] = useState<UploadResult[] | null>(null);

  useEffect(() => {
    if (open) {
      setMode('files');
      setFileList([]);
      setDest('');
      setTimeoutSec(120);
      setResults(null);
      setConfirming(false);
      setRunning(false);
    }
  }, [open]);

  const serverIds = useMemo(() => targets.map((s) => s.id), [targets]);
  const files = useMemo(
    () => fileList.map((f) => f.originFileObj as File | undefined).filter((f): f is File => !!f),
    [fileList],
  );
  const totalBytes = useMemo(() => files.reduce((sum, f) => sum + (f.size || 0), 0), [files]);
  const canRun = files.length > 0 && dest.trim().length > 0 && serverIds.length > 0 && !running;

  async function doRun() {
    if (files.length === 0) return;
    setRunning(true);
    setResults(null);
    try {
      const msg = await uploadFile(serverIds, files, dest.trim(), timeoutSec);
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
      title={t('pages.nodes.upload.title')}
      width="720px"
      okText={confirming ? t('pages.nodes.upload.confirmUpload') : t('pages.nodes.upload.upload')}
      okButtonProps={{ danger: confirming, disabled: !canRun, loading: running }}
      cancelText={confirming ? t('cancel') : t('close')}
      onOk={() => (confirming ? doRun() : setConfirming(true))}
      onCancel={() => (confirming ? setConfirming(false) : close())}
      maskClosable={false}
    >
      <div className="exec-targets">
        <span className="exec-label">{t('pages.nodes.upload.targets', { count: targets.length })}</span>
        <div className="exec-target-tags">
          {targets.map((s) => (
            <Tag key={s.id} color="processing">{s.name || `#${s.id}`}</Tag>
          ))}
        </div>
      </div>

      <span className="exec-label">{t('pages.nodes.upload.file')}</span>
      <Segmented<Mode>
        value={mode}
        onChange={(v) => { setMode(v); setFileList([]); setConfirming(false); }}
        options={[
          { value: 'files', label: t('pages.nodes.upload.modeFiles') },
          { value: 'directory', label: t('pages.nodes.upload.modeDirectory') },
        ]}
        disabled={running}
        style={{ marginBottom: 8 }}
      />
      <Upload
        key={mode}
        fileList={fileList}
        beforeUpload={() => false}
        multiple={mode === 'files'}
        directory={mode === 'directory'}
        onChange={({ fileList: fl }) => { setFileList(fl); setConfirming(false); }}
        disabled={running}
      >
        <Button icon={<UploadOutlined />} disabled={running}>
          {mode === 'directory' ? t('pages.nodes.upload.pickDirectory') : t('pages.nodes.upload.pickFile')}
        </Button>
      </Upload>
      {files.length > 0 && (
        <div className="exec-label" style={{ marginTop: 4 }}>
          {t('pages.nodes.upload.selectedSummary', { count: files.length })} · {formatBytes(totalBytes)}
        </div>
      )}

      <label className="exec-label" htmlFor="upload-dest" style={{ marginTop: 12 }}>{t('pages.nodes.upload.dest')}</label>
      <Tooltip title={mode === 'files' && files.length <= 1 ? t('pages.nodes.upload.destHint') : t('pages.nodes.upload.destDirHint')}>
        <Input
          id="upload-dest"
          value={dest}
          onChange={(e) => { setDest(e.target.value); setConfirming(false); }}
          placeholder={t('pages.nodes.upload.destPlaceholder')}
          disabled={running}
          spellCheck={false}
        />
      </Tooltip>

      <div className="exec-timeout">
        <span className="exec-label">{t('pages.nodes.upload.timeout')}</span>
        <Tooltip title={t('pages.nodes.upload.timeoutHint')}>
          <InputNumber
            min={1}
            max={1800}
            value={timeoutSec}
            onChange={(v) => setTimeoutSec(typeof v === 'number' ? v : 120)}
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
          message={t('pages.nodes.upload.confirmTitle', { count: targets.length })}
          description={t('pages.nodes.upload.confirmBody')}
        />
      )}

      {results && (
        <div className="exec-results">
          {summary && (
            <div className="exec-summary">
              <Tag color="success">{t('pages.nodes.upload.okCount', { count: summary.ok })}</Tag>
              {summary.failed > 0 && <Tag color="error">{t('pages.nodes.upload.failedCount', { count: summary.failed })}</Tag>}
            </div>
          )}
          {results.map((r) => (
            <div key={r.serverId} className="exec-result-row">
              <div className="exec-result-head">
                <span className="exec-result-node">{r.serverName || `#${r.serverId}`}</span>
                <Tag color={statusColor(r.status)}>{t(`pages.nodes.upload.status.${r.status}`)}</Tag>
                {r.status === 'success' && (
                  <span className="exec-result-dur">{t('pages.nodes.upload.filesCount', { count: r.files })} · {formatBytes(r.bytes)} → {r.path}</span>
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

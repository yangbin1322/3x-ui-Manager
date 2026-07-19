import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Input, Modal, Space, Table, Tag, Tooltip, Upload } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DownloadOutlined, UploadOutlined } from '@ant-design/icons';
import type { ManagedServerRecord } from '@/schemas/managedServer';
import type { BulkAddResponse } from '@/generated/types';
import type { Msg } from '@/utils';

interface BulkAddServersModalProps {
  open: boolean;
  createBatch: (servers: Partial<ManagedServerRecord>[]) => Promise<Msg<BulkAddResponse>>;
  onOpenChange: (open: boolean) => void;
}

// Fixed column order for the paste/upload import. A downloadable template
// mirrors this. Password and private key are mutually exclusive per row: a
// non-empty private key selects key auth, otherwise password auth is used.
const COLUMNS = ['name', 'address', 'sshPort', 'sshUser', 'sshPassword', 'sshPrivateKey', 'sshHostKeyMode'] as const;
const TEMPLATE_HEADER = COLUMNS.join(',');
const TEMPLATE_SAMPLE = 'hk-1,203.0.113.5,22,root,secret,,trust';

interface ParsedRow {
  key: number;
  name: string;
  address: string;
  sshPort: number;
  sshUser: string;
  sshPassword: string;
  sshPrivateKey: string;
  sshHostKeyMode: string;
  error?: string;
  outcome?: { success: boolean; message?: string };
}

// splitLine handles both tab-separated (Excel paste) and comma-separated (CSV)
// input. A line with a tab is treated as TSV; otherwise commas split it.
function splitLine(line: string): string[] {
  const raw = line.includes('\t') ? line.split('\t') : line.split(',');
  return raw.map((c) => c.trim());
}

// parseText turns pasted/uploaded text into rows. The first line is treated as a
// header and skipped when its first two cells are literally "name"/"address".
// Rows with no address are dropped (trailing blanks); a row missing every
// credential is kept but flagged so the operator sees why it will fail.
function parseText(text: string, t: (k: string) => string): ParsedRow[] {
  const lines = text.split(/\r?\n/).map((l) => l.trim()).filter((l) => l.length > 0);
  if (lines.length === 0) return [];
  const first = splitLine(lines[0]);
  const hasHeader = first[0]?.toLowerCase() === 'name' && (first[1]?.toLowerCase() === 'address');
  const body = hasHeader ? lines.slice(1) : lines;

  let seq = 0;
  const rows: ParsedRow[] = [];
  for (const line of body) {
    const cells = splitLine(line);
    const [name = '', address = '', sshPort = '', sshUser = '', sshPassword = '', sshPrivateKey = '', sshHostKeyMode = ''] = cells;
    if (address.trim() === '') continue;
    const port = Number(sshPort) || 22;
    const mode = ['trust', 'pin', 'skip'].includes(sshHostKeyMode.trim()) ? sshHostKeyMode.trim() : 'trust';
    const row: ParsedRow = {
      key: seq++,
      name: name.trim(),
      address: address.trim(),
      sshPort: port,
      sshUser: sshUser.trim() || 'root',
      sshPassword: sshPassword.trim(),
      sshPrivateKey: sshPrivateKey.trim(),
      sshHostKeyMode: mode,
    };
    if (row.sshPassword === '' && row.sshPrivateKey === '') {
      row.error = t('pages.servers.bulkNoCredential');
    }
    rows.push(row);
  }
  return rows;
}

export default function BulkAddServersModal({ open, createBatch, onOpenChange }: BulkAddServersModalProps) {
  const { t } = useTranslation();
  const [text, setText] = useState('');
  const [rows, setRows] = useState<ParsedRow[]>([]);
  const [parsed, setParsed] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (open) {
      setText('');
      setRows([]);
      setParsed(false);
      setSubmitting(false);
    }
  }, [open]);

  function doParse(fromText: string) {
    setRows(parseText(fromText, t));
    setParsed(true);
  }

  function onUpload(file: File): boolean {
    const reader = new FileReader();
    reader.onload = () => {
      const content = String(reader.result ?? '');
      setText(content);
      doParse(content);
    };
    reader.readAsText(file);
    return false; // prevent antd's default upload
  }

  function downloadTemplate() {
    const blob = new Blob([`${TEMPLATE_HEADER}\n${TEMPLATE_SAMPLE}\n`], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'servers-template.csv';
    a.click();
    URL.revokeObjectURL(url);
  }

  const validRows = useMemo(() => rows.filter((r) => !r.error), [rows]);
  const canImport = parsed && validRows.length > 0 && !submitting;

  async function submit() {
    setSubmitting(true);
    try {
      const payload: Partial<ManagedServerRecord>[] = validRows.map((r) => ({
        name: r.name,
        address: r.address,
        sshPort: r.sshPort,
        sshUser: r.sshUser,
        sshHostKeyMode: r.sshHostKeyMode as ManagedServerRecord['sshHostKeyMode'],
        ...(r.sshPrivateKey
          ? { sshAuthType: 'key', sshPrivateKey: r.sshPrivateKey }
          : { sshAuthType: 'password', sshPassword: r.sshPassword }),
      }));
      const msg = await createBatch(payload);
      const results = msg?.obj?.results ?? [];
      // Map results (indexed over validRows) back onto row keys for inline marks.
      setRows((prev) => prev.map((r) => {
        const idx = validRows.findIndex((v) => v.key === r.key);
        if (idx < 0) return r;
        const res = results[idx];
        return res ? { ...r, outcome: { success: res.success, message: res.message } } : r;
      }));
      if (results.length > 0 && results.every((r) => r.success)) {
        onOpenChange(false);
      }
    } finally {
      setSubmitting(false);
    }
  }

  const columns: ColumnsType<ParsedRow> = [
    {
      title: t('pages.nodes.name'),
      dataIndex: 'name',
      width: 130,
      render: (v: string, r) => v || <span style={{ opacity: 0.5 }}>{r.address}</span>,
    },
    { title: t('pages.nodes.address'), dataIndex: 'address', width: 150 },
    { title: t('pages.nodes.sshPort'), dataIndex: 'sshPort', width: 80 },
    { title: t('pages.nodes.sshUser'), dataIndex: 'sshUser', width: 100 },
    {
      title: t('pages.nodes.sshAuthType'),
      width: 100,
      render: (_v, r) => r.sshPrivateKey ? t('pages.nodes.sshAuthKey') : t('pages.nodes.sshAuthPassword'),
    },
    { title: t('pages.nodes.sshHostKeyMode'), dataIndex: 'sshHostKeyMode', width: 110 },
    {
      title: '',
      width: 70,
      render: (_v, r) => {
        if (r.outcome) {
          return r.outcome.success
            ? <Tag color="success">OK</Tag>
            : <Tooltip title={r.outcome.message}><Tag color="error">!</Tag></Tooltip>;
        }
        if (r.error) return <Tooltip title={r.error}><Tag color="warning">!</Tag></Tooltip>;
        return null;
      },
    },
  ];

  const errorCount = rows.filter((r) => r.error).length;
  const failedCount = rows.filter((r) => r.outcome && !r.outcome.success).length;

  return (
    <Modal
      open={open}
      title={t('pages.servers.bulkAdd')}
      width="900px"
      okText={t('pages.servers.bulkImport', { count: validRows.length })}
      cancelText={t('cancel')}
      okButtonProps={{ disabled: !canImport, loading: submitting }}
      onOk={submit}
      onCancel={() => { if (!submitting) onOpenChange(false); }}
      maskClosable={false}
    >
      <Alert type="info" showIcon style={{ marginBottom: 12 }} message={t('pages.servers.bulkAddHint')} />

      <Space style={{ marginBottom: 8 }}>
        <Button icon={<DownloadOutlined />} onClick={downloadTemplate}>
          {t('pages.servers.bulkTemplate')}
        </Button>
        <Upload accept=".csv,.txt" showUploadList={false} beforeUpload={onUpload}>
          <Button icon={<UploadOutlined />}>{t('pages.servers.bulkUpload')}</Button>
        </Upload>
      </Space>

      <Input.TextArea
        value={text}
        onChange={(e) => { setText(e.target.value); setParsed(false); }}
        placeholder={`${TEMPLATE_HEADER}\n${TEMPLATE_SAMPLE}`}
        autoSize={{ minRows: 4, maxRows: 8 }}
        spellCheck={false}
        style={{ fontFamily: 'monospace' }}
      />
      <Button type="primary" ghost onClick={() => doParse(text)} disabled={text.trim().length === 0} style={{ marginTop: 8 }}>
        {t('pages.servers.bulkParse')}
      </Button>

      {parsed && (
        <div style={{ marginTop: 12 }}>
          {rows.length === 0 ? (
            <Alert type="warning" showIcon message={t('pages.servers.bulkNothingParsed')} />
          ) : (
            <>
              <Table<ParsedRow>
                rowKey="key"
                size="small"
                columns={columns}
                dataSource={rows}
                pagination={false}
                scroll={{ x: 'max-content', y: 300 }}
              />
              {errorCount > 0 && !failedCount && (
                <Alert type="warning" showIcon style={{ marginTop: 8 }} message={t('pages.servers.bulkParseErrors', { count: errorCount })} />
              )}
              {failedCount > 0 && (
                <Alert type="warning" showIcon style={{ marginTop: 8 }} message={t('pages.servers.bulkAddFailed', { count: failedCount })} />
              )}
            </>
          )}
        </div>
      )}
    </Modal>
  );
}

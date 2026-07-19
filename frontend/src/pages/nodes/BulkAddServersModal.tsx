import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Input, Modal, Select, Space, Table, Tag, Tooltip } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';
import type { ManagedServerRecord } from '@/schemas/managedServer';
import type { BulkAddResponse } from '@/generated/types';
import type { Msg } from '@/utils';

interface BulkAddServersModalProps {
  open: boolean;
  createBatch: (servers: Partial<ManagedServerRecord>[]) => Promise<Msg<BulkAddResponse>>;
  onOpenChange: (open: boolean) => void;
}

type AuthType = 'password' | 'key';
type HostKeyMode = 'trust' | 'pin' | 'skip';

interface Row {
  key: number;
  name: string;
  address: string;
  sshPort: number;
  sshUser: string;
  sshAuthType: AuthType;
  sshPassword: string;
  sshPrivateKey: string;
  sshHostKeyMode: HostKeyMode;
}

interface RowOutcome {
  success: boolean;
  message?: string;
}

let rowSeq = 0;
function blankRow(): Row {
  return {
    key: rowSeq++,
    name: '',
    address: '',
    sshPort: 22,
    sshUser: 'root',
    sshAuthType: 'password',
    sshPassword: '',
    sshPrivateKey: '',
    sshHostKeyMode: 'trust',
  };
}

export default function BulkAddServersModal({ open, createBatch, onOpenChange }: BulkAddServersModalProps) {
  const { t } = useTranslation();
  const [rows, setRows] = useState<Row[]>([blankRow()]);
  const [submitting, setSubmitting] = useState(false);
  // Per-row outcomes from the last submit, keyed by row key. Cleared on edit.
  const [outcomes, setOutcomes] = useState<Record<number, RowOutcome>>({});

  useEffect(() => {
    if (open) {
      setRows([blankRow()]);
      setOutcomes({});
      setSubmitting(false);
    }
  }, [open]);

  function patchRow(key: number, patch: Partial<Row>) {
    setRows((prev) => prev.map((r) => (r.key === key ? { ...r, ...patch } : r)));
    setOutcomes({});
  }

  function addRow() {
    setRows((prev) => [...prev, blankRow()]);
  }

  function removeRow(key: number) {
    setRows((prev) => (prev.length > 1 ? prev.filter((r) => r.key !== key) : prev));
  }

  // A row is fillable once it has an address and the credential its auth type
  // needs. Rows with no address at all are dropped silently (trailing blanks).
  const submittable = useMemo(
    () => rows.filter((r) => r.address.trim().length > 0),
    [rows],
  );
  const canSubmit = submittable.length > 0 && !submitting;

  async function submit() {
    setSubmitting(true);
    setOutcomes({});
    try {
      const payload: Partial<ManagedServerRecord>[] = submittable.map((r) => ({
        name: r.name.trim(),
        address: r.address.trim(),
        sshPort: r.sshPort,
        sshUser: r.sshUser.trim(),
        sshAuthType: r.sshAuthType,
        sshHostKeyMode: r.sshHostKeyMode,
        ...(r.sshAuthType === 'password' ? { sshPassword: r.sshPassword } : { sshPrivateKey: r.sshPrivateKey }),
      }));
      const msg = await createBatch(payload);
      const results = msg?.obj?.results ?? [];
      // Map results (indexed over submittable) back onto row keys.
      const next: Record<number, RowOutcome> = {};
      results.forEach((res, i) => {
        const row = submittable[i];
        if (row) next[row.key] = { success: res.success, message: res.message };
      });
      setOutcomes(next);
      // If every submitted row succeeded, close; otherwise keep the modal open
      // so the operator can fix the failed rows.
      if (results.length > 0 && results.every((r) => r.success)) {
        onOpenChange(false);
      }
    } finally {
      setSubmitting(false);
    }
  }

  const columns: ColumnsType<Row> = [
    {
      title: t('pages.nodes.name'),
      width: 130,
      render: (_v, r) => (
        <Input
          value={r.name}
          placeholder={t('pages.servers.bulkNamePlaceholder')}
          onChange={(e) => patchRow(r.key, { name: e.target.value })}
        />
      ),
    },
    {
      title: t('pages.nodes.address'),
      width: 150,
      render: (_v, r) => (
        <Input
          value={r.address}
          placeholder="203.0.113.5"
          onChange={(e) => patchRow(r.key, { address: e.target.value })}
        />
      ),
    },
    {
      title: t('pages.nodes.sshPort'),
      width: 90,
      render: (_v, r) => (
        <Input
          type="number"
          value={r.sshPort}
          onChange={(e) => patchRow(r.key, { sshPort: Number(e.target.value) || 22 })}
        />
      ),
    },
    {
      title: t('pages.nodes.sshUser'),
      width: 110,
      render: (_v, r) => (
        <Input value={r.sshUser} onChange={(e) => patchRow(r.key, { sshUser: e.target.value })} />
      ),
    },
    {
      title: t('pages.nodes.sshAuthType'),
      width: 120,
      render: (_v, r) => (
        <Select<AuthType>
          value={r.sshAuthType}
          style={{ width: '100%' }}
          onChange={(v) => patchRow(r.key, { sshAuthType: v })}
          options={[
            { value: 'password', label: t('pages.nodes.sshAuthPassword') },
            { value: 'key', label: t('pages.nodes.sshAuthKey') },
          ]}
        />
      ),
    },
    {
      title: t('pages.nodes.sshPassword'),
      width: 180,
      render: (_v, r) => r.sshAuthType === 'password' ? (
        <Input.Password
          value={r.sshPassword}
          autoComplete="new-password"
          onChange={(e) => patchRow(r.key, { sshPassword: e.target.value })}
        />
      ) : (
        <Input.TextArea
          value={r.sshPrivateKey}
          autoSize={{ minRows: 1, maxRows: 3 }}
          placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
          onChange={(e) => patchRow(r.key, { sshPrivateKey: e.target.value })}
        />
      ),
    },
    {
      title: t('pages.nodes.sshHostKeyMode'),
      width: 130,
      render: (_v, r) => (
        <Select<HostKeyMode>
          value={r.sshHostKeyMode}
          style={{ width: '100%' }}
          onChange={(v) => patchRow(r.key, { sshHostKeyMode: v })}
          options={[
            { value: 'trust', label: t('pages.nodes.sshHostKeyTrust') },
            { value: 'skip', label: t('pages.nodes.sshHostKeySkip') },
          ]}
        />
      ),
    },
    {
      title: '',
      width: 80,
      render: (_v, r) => {
        const outcome = outcomes[r.key];
        return (
          <Space size={4}>
            {outcome && (outcome.success ? (
              <Tag color="success">OK</Tag>
            ) : (
              <Tooltip title={outcome.message}>
                <Tag color="error">!</Tag>
              </Tooltip>
            ))}
            <Button
              type="text"
              size="small"
              danger
              icon={<DeleteOutlined />}
              disabled={rows.length <= 1}
              aria-label={t('delete')}
              onClick={() => removeRow(r.key)}
            />
          </Space>
        );
      },
    },
  ];

  const failedCount = Object.values(outcomes).filter((o) => !o.success).length;

  return (
    <Modal
      open={open}
      title={t('pages.servers.bulkAdd')}
      width="960px"
      okText={t('save')}
      cancelText={t('cancel')}
      okButtonProps={{ disabled: !canSubmit, loading: submitting }}
      onOk={submit}
      onCancel={() => { if (!submitting) onOpenChange(false); }}
      maskClosable={false}
    >
      <Alert type="info" showIcon style={{ marginBottom: 12 }} message={t('pages.servers.bulkAddHint')} />
      <Table<Row>
        rowKey="key"
        size="small"
        columns={columns}
        dataSource={rows}
        pagination={false}
        scroll={{ x: 'max-content', y: 360 }}
      />
      <Button type="dashed" icon={<PlusOutlined />} onClick={addRow} style={{ marginTop: 12 }} block>
        {t('pages.servers.bulkAddRow')}
      </Button>
      {failedCount > 0 && (
        <Alert type="warning" showIcon style={{ marginTop: 12 }} message={t('pages.servers.bulkAddFailed', { count: failedCount })} />
      )}
    </Modal>
  );
}

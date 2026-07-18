import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Modal, Table, Tag, Tooltip } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { CommandExecution } from '@/generated/types';
import type { ExecHistoryFilter } from '@/api/queries/useManagedServerMutations';
import type { Msg } from '@/utils';
import type { ExecHistoryResponse } from '@/generated/types';
import './ExecHistoryModal.css';

interface ExecHistoryModalProps {
  open: boolean;
  fetchHistory: (filter: ExecHistoryFilter) => Promise<Msg<ExecHistoryResponse>>;
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

const PAGE_SIZE = 20;

export default function ExecHistoryModal({ open, fetchHistory, onOpenChange }: ExecHistoryModalProps) {
  const { t } = useTranslation();
  const [rows, setRows] = useState<CommandExecution[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async (p: number) => {
    setLoading(true);
    try {
      const msg = await fetchHistory({ page: p, pageSize: PAGE_SIZE });
      if (msg?.success && msg.obj) {
        setRows(msg.obj.items ?? []);
        setTotal(msg.obj.total ?? 0);
      } else {
        setRows([]);
        setTotal(0);
      }
    } finally {
      setLoading(false);
    }
  }, [fetchHistory]);

  useEffect(() => {
    if (open) {
      setPage(1);
      void load(1);
    }
  }, [open, load]);

  const columns: ColumnsType<CommandExecution> = [
    {
      title: t('pages.nodes.exec.history.time'),
      dataIndex: 'createdAt',
      width: 160,
      render: (v: number) => new Date(v).toLocaleString(),
    },
    { title: t('pages.nodes.name'), dataIndex: 'serverName', width: 120, ellipsis: true },
    { title: t('pages.nodes.exec.history.user'), dataIndex: 'username', width: 110, ellipsis: true },
    {
      title: t('pages.nodes.exec.command'),
      dataIndex: 'command',
      ellipsis: true,
      render: (v: string) => <span className="exec-hist-cmd">{v}</span>,
    },
    {
      title: t('pages.nodes.exec.history.status'),
      dataIndex: 'status',
      width: 120,
      render: (v: string, r) => (
        <Tooltip title={r.exitCode !== 0 ? `exit ${r.exitCode}` : undefined}>
          <Tag color={statusColor(v)}>{t(`pages.nodes.exec.status.${v}`)}</Tag>
        </Tooltip>
      ),
    },
  ];

  return (
    <Modal
      open={open}
      title={t('pages.nodes.exec.history.title')}
      width="920px"
      footer={null}
      onCancel={() => onOpenChange(false)}
    >
      <Table<CommandExecution>
        rowKey="id"
        size="small"
        loading={loading}
        columns={columns}
        dataSource={rows}
        expandable={{
          expandedRowRender: (r) => (
            <pre className="exec-hist-out">{r.error ? `${r.error}\n${r.stdout}` : (r.stdout || '(no output)')}</pre>
          ),
          rowExpandable: (r) => !!(r.stdout || r.error),
        }}
        pagination={{
          current: page,
          pageSize: PAGE_SIZE,
          total,
          showSizeChanger: false,
          onChange: (p) => { setPage(p); void load(p); },
        }}
      />
    </Modal>
  );
}

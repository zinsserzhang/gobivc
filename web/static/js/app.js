// ===== GobiVC Frontend Application =====

const API_BASE = '/api';
let currentReportId = null;
let pollTimer = null;
let genStartTime = null;
let genTimeTimer = null;

// ===== View Management =====

function switchView(view) {
    document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
    document.querySelectorAll('.nav-btn').forEach(b => b.classList.remove('active'));

    document.getElementById('view-' + view).classList.add('active');
    const navBtn = document.querySelector(`.nav-btn[data-view="${view}"]`);
    if (navBtn) navBtn.classList.add('active');

    if (view === 'list') {
        loadReports();
    }

    // Clear any polling when navigating away from generating
    if (view !== 'generating') {
        stopPolling();
    }
}

// ===== API Calls =====

async function apiCall(url, options = {}) {
    const resp = await fetch(API_BASE + url, {
        headers: { 'Content-Type': 'application/json' },
        ...options,
    });
    const data = await resp.json();
    if (!resp.ok) {
        throw new Error(data.error || 'API request failed');
    }
    return data;
}

// ===== Report Creation =====

async function handleSubmit(event) {
    event.preventDefault();
    const btn = document.getElementById('btn-submit');
    btn.disabled = true;
    btn.textContent = '提交中...';

    try {
        const form = document.getElementById('report-form');
        const topic = form.topic.value.trim();
        const direction = form.direction.value.trim();
        const depth = form.querySelector('input[name="depth"]:checked').value;
        const customNotes = form.custom_notes.value.trim();

        const report = await apiCall('/reports', {
            method: 'POST',
            body: JSON.stringify({ topic, direction, depth, custom_notes: customNotes }),
        });

        currentReportId = report.id;
        document.getElementById('generating-topic').textContent = topic;
        switchView('generating');
        startPolling(report.id);
        startGenTimer();
        form.reset();
        form.querySelector('input[name="depth"][value="standard"]').checked = true;
    } catch (err) {
        alert('创建报告失败: ' + err.message);
    } finally {
        btn.disabled = false;
        btn.textContent = '生成报告';
    }
}

// ===== Polling for Report Status =====

function startPolling(reportId) {
    stopPolling();
    let elapsed = 0;
    pollTimer = setInterval(async () => {
        try {
            const report = await apiCall('/reports/' + reportId);
            elapsed++;
            // Update progress bar (estimate: brief ~30s, standard ~60s, deep ~120s)
            const maxTime = report.config.depth === 'brief' ? 30 : report.config.depth === 'deep' ? 120 : 60;
            const progress = Math.min(95, (elapsed / maxTime) * 100);
            document.getElementById('progress-fill').style.width = progress + '%';

            if (report.status === 'completed') {
                document.getElementById('progress-fill').style.width = '100%';
                stopPolling();
                setTimeout(() => viewReport(reportId), 500);
            } else if (report.status === 'failed') {
                stopPolling();
                alert('报告生成失败: ' + (report.error_msg || '未知错误'));
                switchView('create');
            }
        } catch (err) {
            console.error('Polling error:', err);
        }
    }, 2000);
}

function stopPolling() {
    if (pollTimer) {
        clearInterval(pollTimer);
        pollTimer = null;
    }
    if (genTimeTimer) {
        clearInterval(genTimeTimer);
        genTimeTimer = null;
    }
}

function startGenTimer() {
    genStartTime = Date.now();
    const el = document.getElementById('generating-time');
    genTimeTimer = setInterval(() => {
        const seconds = Math.floor((Date.now() - genStartTime) / 1000);
        if (seconds < 60) {
            el.textContent = `已用时: ${seconds}秒`;
        } else {
            const mins = Math.floor(seconds / 60);
            const secs = seconds % 60;
            el.textContent = `已用时: ${mins}分${secs}秒`;
        }
    }, 1000);
}

// ===== Report List =====

async function loadReports() {
    const container = document.getElementById('report-list');
    try {
        const reports = await apiCall('/reports');
        if (!reports || reports.length === 0) {
            container.innerHTML = '<div class="empty-state"><p>暂无报告，请先生成一份行业研究报告</p></div>';
            return;
        }
        container.innerHTML = reports.map(r => `
            <div class="report-card" onclick="viewReport('${r.id}')">
                <div class="report-card-info">
                    <div class="report-card-title">${escapeHtml(r.title || r.topic)}</div>
                    <div class="report-card-meta">
                        <span>${escapeHtml(r.topic)}</span>
                        ${r.direction ? `<span>${escapeHtml(r.direction)}</span>` : ''}
                        <span class="depth-badge">${depthLabel(r.depth)}</span>
                        <span>${formatTime(r.created_at)}</span>
                    </div>
                </div>
                <div class="report-card-actions">
                    <span class="status-badge status-${r.status}">${statusLabel(r.status)}</span>
                    <button class="btn-danger" onclick="event.stopPropagation(); deleteReport('${r.id}')">删除</button>
                </div>
            </div>
        `).join('');
    } catch (err) {
        container.innerHTML = '<div class="empty-state"><p>加载失败: ' + escapeHtml(err.message) + '</p></div>';
    }
}

// ===== Report Detail =====

async function viewReport(id) {
    try {
        const report = await apiCall('/reports/' + id);
        if (report.status === 'generating' || report.status === 'pending') {
            currentReportId = id;
            document.getElementById('generating-topic').textContent = report.config.topic;
            switchView('generating');
            startPolling(id);
            startGenTimer();
            return;
        }

        document.getElementById('detail-header').innerHTML = `
            <div>
                <h2>${escapeHtml(report.title || report.config.topic)}</h2>
                <p style="color:var(--text-secondary);font-size:13px;margin-top:4px;">
                    ${escapeHtml(report.config.topic)}
                    ${report.config.direction ? ' · ' + escapeHtml(report.config.direction) : ''}
                    · ${depthLabel(report.config.depth)}
                    · ${formatTime(report.created_at)}
                </p>
            </div>
            <div class="detail-actions">
                <button class="btn-secondary" onclick="copyReport('${id}')">复制内容</button>
                <button class="btn-secondary" onclick="downloadReport('${id}')">下载 Markdown</button>
            </div>
        `;

        const detailEl = document.getElementById('report-detail');
        if (report.status === 'failed') {
            detailEl.innerHTML = `<div class="empty-state"><p>报告生成失败: ${escapeHtml(report.error_msg || '未知错误')}</p></div>`;
        } else {
            detailEl.innerHTML = renderMarkdown(report.content || '');
        }

        switchView('detail');
    } catch (err) {
        alert('加载报告失败: ' + err.message);
    }
}

// ===== Report Actions =====

async function deleteReport(id) {
    if (!confirm('确定要删除这份报告吗？')) return;
    try {
        await apiCall('/reports/' + id, { method: 'DELETE' });
        loadReports();
    } catch (err) {
        alert('删除失败: ' + err.message);
    }
}

async function copyReport(id) {
    try {
        const report = await apiCall('/reports/' + id);
        await navigator.clipboard.writeText(report.content || '');
        alert('报告内容已复制到剪贴板');
    } catch (err) {
        alert('复制失败: ' + err.message);
    }
}

async function downloadReport(id) {
    try {
        const report = await apiCall('/reports/' + id);
        const blob = new Blob([report.content || ''], { type: 'text/markdown;charset=utf-8' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = (report.title || 'report') + '.md';
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
    } catch (err) {
        alert('下载失败: ' + err.message);
    }
}

// ===== Simple Markdown Renderer =====

function renderMarkdown(md) {
    if (!md) return '';
    let html = escapeHtml(md);

    // Headers
    html = html.replace(/^### (.+)$/gm, '<h3>$1</h3>');
    html = html.replace(/^## (.+)$/gm, '<h2>$1</h2>');
    html = html.replace(/^# (.+)$/gm, '<h1>$1</h1>');

    // Bold and italic
    html = html.replace(/\*\*\*(.+?)\*\*\*/g, '<strong><em>$1</em></strong>');
    html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
    html = html.replace(/\*(.+?)\*/g, '<em>$1</em>');

    // Blockquotes
    html = html.replace(/^&gt; (.+)$/gm, '<blockquote>$1</blockquote>');

    // Tables
    html = html.replace(/^\|(.+)\|$/gm, function(match, content) {
        const cells = content.split('|').map(c => c.trim());
        if (cells.every(c => /^[-:]+$/.test(c))) {
            return '<!--table-sep-->';
        }
        return '<tr>' + cells.map(c => `<td>${c}</td>`).join('') + '</tr>';
    });
    // Wrap consecutive <tr> rows in table
    html = html.replace(/((<tr>.+<\/tr>\n?)+)/g, function(match) {
        let cleaned = match.replace(/<!--table-sep-->\n?/g, '');
        if (!cleaned.trim()) return '';
        // Convert first row to th
        cleaned = cleaned.replace(/<tr>(.+?)<\/tr>/, function(m, inner) {
            return '<thead><tr>' + inner.replace(/<td>/g, '<th>').replace(/<\/td>/g, '</th>') + '</tr></thead><tbody>';
        });
        return '<table>' + cleaned + '</tbody></table>';
    });
    html = html.replace(/<!--table-sep-->\n?/g, '');

    // Unordered lists
    html = html.replace(/^- (.+)$/gm, '<li>$1</li>');
    html = html.replace(/((<li>.+<\/li>\n?)+)/g, '<ul>$1</ul>');

    // Ordered lists
    html = html.replace(/^\d+\. (.+)$/gm, '<li>$1</li>');

    // Horizontal rule
    html = html.replace(/^---$/gm, '<hr>');

    // Inline code
    html = html.replace(/`(.+?)`/g, '<code>$1</code>');

    // Paragraphs: wrap lines that aren't already HTML tags
    html = html.split('\n\n').map(block => {
        block = block.trim();
        if (!block) return '';
        if (/^<[a-z]/.test(block)) return block;
        if (/^<\//.test(block)) return block;
        return '<p>' + block.replace(/\n/g, '<br>') + '</p>';
    }).join('\n');

    return html;
}

// ===== Utility Functions =====

function escapeHtml(str) {
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

function statusLabel(status) {
    const labels = {
        pending: '等待中',
        generating: '生成中',
        completed: '已完成',
        failed: '失败',
    };
    return labels[status] || status;
}

function depthLabel(depth) {
    const labels = {
        brief: '概览',
        standard: '标准',
        deep: '深度',
    };
    return labels[depth] || depth;
}

function formatTime(ts) {
    if (!ts) return '';
    const d = new Date(ts);
    const pad = n => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

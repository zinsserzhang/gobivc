// ===== GobiVC Frontend Application =====

const API_BASE = '/api';
const TOKEN_KEY = 'gobivc_api_token';

let currentReportId = null;
let pollTimer = null;
let genStartTime = null;
let genTimeTimer = null;
let eventSource = null;
let streamContent = '';
let searchTimer = null;
let allReports = []; // cached report list for client-side filtering

// ===== Token Management =====
function getApiToken() { return localStorage.getItem(TOKEN_KEY) || ''; }
function setApiToken(token) { token ? localStorage.setItem(TOKEN_KEY, token) : localStorage.removeItem(TOKEN_KEY); }

function promptForToken() {
    const current = getApiToken();
    const token = prompt('请输入 API Token（留空表示未启用鉴权）：', current);
    if (token !== null) {
        setApiToken(token.trim());
        toast('Token 已保存', 'success');
    }
}

// ===== Toast Notifications =====
function toast(message, type = 'info', duration = 3000) {
    const container = document.getElementById('toast-container');
    const el = document.createElement('div');
    el.className = `toast toast-${type}`;
    el.textContent = message;
    container.appendChild(el);
    setTimeout(() => {
        el.classList.add('toast-exit');
        setTimeout(() => el.remove(), 300);
    }, duration);
}

// ===== Mobile Sidebar =====
function toggleSidebar() {
    const sidebar = document.getElementById('sidebar');
    sidebar.classList.toggle('open');
    // Overlay
    let overlay = document.querySelector('.sidebar-overlay');
    if (sidebar.classList.contains('open')) {
        if (!overlay) {
            overlay = document.createElement('div');
            overlay.className = 'sidebar-overlay';
            overlay.style.display = 'block';
            overlay.onclick = () => toggleSidebar();
            document.body.appendChild(overlay);
        } else {
            overlay.style.display = 'block';
        }
    } else if (overlay) {
        overlay.style.display = 'none';
    }
}

// ===== View Management =====
let currentTypeFilter = '';

function switchView(view) {
    document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
    document.querySelectorAll('.nav-btn[data-view]').forEach(b => b.classList.remove('active'));
    document.getElementById('view-' + view).classList.add('active');
    const navBtn = document.querySelector(`.nav-btn[data-view="${view}"]`);
    if (navBtn) navBtn.classList.add('active');

    if (view === 'list') loadReports();
    if (view !== 'generating') { stopPolling(); stopStream(); }

    const sidebar = document.getElementById('sidebar');
    if (sidebar.classList.contains('open')) toggleSidebar();
    window.scrollTo(0, 0);
}

// Open create view for a specific report type
function openCreate(type) {
    const cfg = {
        industry:  { title: '行业研究报告', desc: '配置研究参数，AI 将为您生成专业的行业研究报告', topicLabel: '研究主题', topicPh: '输入行业或细分领域...', dirLabel: '研究方向', dirPh: '例如：市场规模与增长趋势、竞争格局分析...', dirHint: '可选，指定报告的重点分析方向', showFiles: false, showSuggestions: true },
        memo:      { title: '立项报告', desc: '基于项目材料生成投委会立项报告 / 投资备忘录', topicLabel: '项目名称', topicPh: '例如：XX科技 A轮融资项目...', dirLabel: '侧重方向', dirPh: '例如：重点分析商业模式和财务数据...', dirHint: '可选，指定报告侧重的分析方向', showFiles: true, showSuggestions: false },
        checklist: { title: '尽调清单', desc: '基于项目材料生成投资尽职调查清单', topicLabel: '项目名称', topicPh: '例如：XX科技 A轮融资项目...', dirLabel: '侧重方向', dirPh: '例如：重点关注财务真实性和合规风险...', dirHint: '可选，指定尽调重点关注领域', showFiles: true, showSuggestions: false },
        questions: { title: '核心问题关注', desc: '基于项目材料梳理投资决策的核心问题', topicLabel: '项目名称', topicPh: '例如：XX科技 A轮融资项目...', dirLabel: '关注领域', dirPh: '例如：技术壁垒、团队稳定性、客户集中度...', dirHint: '可选，指定需要重点关注的问题领域', showFiles: true, showSuggestions: false },
    };

    const c = cfg[type] || cfg.industry;
    document.getElementById('report-type').value = type;
    document.getElementById('create-header').innerHTML = `<h2>${c.title}</h2><p>${c.desc}</p>`;
    document.getElementById('topic-label').innerHTML = c.topicLabel + ' <span class="required">*</span>';
    document.getElementById('topic').placeholder = c.topicPh;
    document.getElementById('direction-label').textContent = c.dirLabel;
    document.getElementById('direction').placeholder = c.dirPh;
    document.getElementById('direction-hint').textContent = c.dirHint;
    document.getElementById('topic-suggestions').style.display = c.showSuggestions ? 'block' : 'none';
    document.getElementById('file-upload-group').style.display = c.showFiles ? 'block' : 'none';

    // Highlight sidebar
    document.querySelectorAll('.nav-btn[data-view]').forEach(b => b.classList.remove('active'));
    const navBtn = document.querySelector(`.nav-btn[data-view="create-${type}"]`);
    if (navBtn) navBtn.classList.add('active');

    document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
    document.getElementById('view-create').classList.add('active');
    window.scrollTo(0, 0);
}

// Type tab filter for report list
function filterByType(type) {
    currentTypeFilter = type;
    document.querySelectorAll('.type-tab').forEach(t => t.classList.remove('active'));
    document.querySelector(`.type-tab[data-type="${type}"]`).classList.add('active');
    applyFilters();
}

// ===== API =====
async function apiCall(url, options = {}) {
    const headers = { 'Content-Type': 'application/json', ...(options.headers || {}) };
    const token = getApiToken();
    if (token) headers['Authorization'] = 'Bearer ' + token;
    const resp = await fetch(API_BASE + url, { ...options, headers });
    const data = await resp.json();
    if (!resp.ok) {
        if (resp.status === 401) throw new Error('未授权：请配置正确的 API Token');
        throw new Error(data.error || '请求失败');
    }
    return data;
}

// ===== Check Feishu Status =====
async function checkFeishuStatus() {
    try {
        const resp = await fetch('/health');
        const data = await resp.json();
        if (data.feishu_enabled) {
            document.getElementById('feishu-group').style.display = 'block';
            document.getElementById('use-feishu').checked = true; // default on
        }
    } catch (e) { /* ignore */ }
}
checkFeishuStatus();

// ===== File Upload =====
let uploadedFileIDs = [];
function handleFileSelect(event) {
    const files = event.target.files;
    if (files.length > 0) uploadFiles(files);
    event.target.value = '';
}

function handleDrop(event) {
    event.preventDefault();
    event.target.classList.remove('drag-over');
    const files = event.dataTransfer.files;
    if (files.length > 0) uploadFiles(files);
}

async function uploadFiles(files) {
    const formData = new FormData();
    for (const f of files) {
        formData.append('files', f);
    }

    const listEl = document.getElementById('file-list');

    // Show uploading state
    for (const f of files) {
        const el = document.createElement('div');
        el.className = 'file-item';
        el.id = 'file-pending-' + f.name;
        el.innerHTML = `
            <span class="file-item-name">${escapeHtml(f.name)}</span>
            <span class="file-item-size">${formatFileSize(f.size)}</span>
            <span class="file-item-status uploading">上传中...</span>
        `;
        listEl.appendChild(el);
    }

    try {
        const token = getApiToken();
        const headers = {};
        if (token) headers['Authorization'] = 'Bearer ' + token;

        const resp = await fetch('/api/uploads', { method: 'POST', headers, body: formData });
        const data = await resp.json();

        if (!resp.ok) throw new Error(data.error || '上传失败');

        // Remove pending items
        for (const f of files) {
            const el = document.getElementById('file-pending-' + f.name);
            if (el) el.remove();
        }

        // Add successful items
        for (const uf of data) {
            uploadedFileIDs.push(uf.id);
            const el = document.createElement('div');
            el.className = 'file-item';
            el.dataset.fileId = uf.id;
            const textLen = uf.text ? uf.text.length : 0;
            el.innerHTML = `
                <span class="file-item-name">${escapeHtml(uf.name)}</span>
                <span class="file-item-size">${formatFileSize(uf.size)}</span>
                <span class="file-item-status success">${textLen > 0 ? '已提取 ' + textLen + ' 字' : '已上传'}</span>
                <button type="button" class="file-item-remove" onclick="removeFile('${uf.id}', this)">&#10005;</button>
            `;
            listEl.appendChild(el);
        }
        toast(`成功上传 ${data.length} 个文件`, 'success');
    } catch (err) {
        // Mark as failed
        for (const f of files) {
            const el = document.getElementById('file-pending-' + f.name);
            if (el) {
                el.querySelector('.file-item-status').className = 'file-item-status error';
                el.querySelector('.file-item-status').textContent = '失败';
            }
        }
        toast('上传失败: ' + err.message, 'error');
    }
}

function removeFile(fileId, btn) {
    uploadedFileIDs = uploadedFileIDs.filter(id => id !== fileId);
    btn.closest('.file-item').remove();
}

function formatFileSize(bytes) {
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024*1024) return (bytes/1024).toFixed(1) + ' KB';
    return (bytes/1024/1024).toFixed(1) + ' MB';
}

// ===== Topic Suggestions =====
function fillTopic(topic) {
    document.getElementById('topic').value = topic;
    document.getElementById('topic').focus();
}

// ===== Report Creation =====
async function handleSubmit(event) {
    event.preventDefault();
    const btn = document.getElementById('btn-submit');
    const btnText = btn.querySelector('.btn-text');
    const btnLoading = btn.querySelector('.btn-loading');

    btn.disabled = true;
    btnText.style.display = 'none';
    btnLoading.style.display = 'inline-flex';

    try {
        const form = document.getElementById('report-form');
        const reportType = document.getElementById('report-type').value;
        const topic = form.topic.value.trim();
        const direction = form.direction.value.trim();
        const depth = form.querySelector('input[name="depth"]:checked').value;
        const customNotes = form.custom_notes.value.trim();

        if (!topic) { toast('请输入主题/项目名称', 'error'); return; }

        const useFeishu = document.getElementById('use-feishu')?.checked || false;

        const report = await apiCall('/reports', {
            method: 'POST',
            body: JSON.stringify({
                report_type: reportType,
                topic, direction, depth,
                custom_notes: customNotes,
                use_feishu: useFeishu,
                file_ids: uploadedFileIDs,
            }),
        });

        currentReportId = report.id;
        document.getElementById('generating-topic').textContent = topic;
        document.getElementById('progress-fill').style.width = '0%';
        streamContent = '';
        const streamEl = document.getElementById('stream-output');
        streamEl.innerHTML = '<div class="report-body"><div class="stream-placeholder"><div class="stream-placeholder-icon">&#129302;</div><p>AI 正在分析行业数据并撰写报告</p><p class="stream-placeholder-sub">内容将实时显示在此处...</p></div></div>';

        switchView('generating');
        startGenTimer();
        startStream(report.id);
        toast('报告生成已启动', 'success');

        form.reset();
        form.querySelector('input[name="depth"][value="standard"]').checked = true;
        uploadedFileIDs = [];
        document.getElementById('file-list').innerHTML = '';
    } catch (err) {
        toast('创建失败: ' + err.message, 'error');
    } finally {
        btn.disabled = false;
        btnText.style.display = 'inline';
        btnLoading.style.display = 'none';
    }
}

// ===== SSE Streaming =====
function startStream(reportId) {
    stopStream();
    streamContent = '';

    const token = getApiToken();
    let streamUrl = API_BASE + '/reports/' + reportId + '/stream';
    if (token) streamUrl += '?token=' + encodeURIComponent(token);
    eventSource = new EventSource(streamUrl);

    eventSource.onmessage = function(event) {
        let data;
        try { data = JSON.parse(event.data); } catch (e) { return; }

        const outputEl = document.getElementById('stream-output');

        switch (data.type) {
            case 'start':
                outputEl.innerHTML = '<div class="report-body"></div>';
                break;
            case 'chunk':
                streamContent += data.text;
                outputEl.querySelector('.report-body').innerHTML = renderMarkdown(streamContent);
                outputEl.scrollTop = outputEl.scrollHeight;
                updateStreamProgress();
                break;
            case 'content':
                streamContent = data.text;
                outputEl.querySelector('.report-body').innerHTML = renderMarkdown(streamContent);
                stopStream(); stopGenTimer();
                setTimeout(() => viewReport(reportId), 300);
                break;
            case 'done':
                document.getElementById('progress-fill').style.width = '100%';
                stopStream(); stopGenTimer();
                toast('报告生成完成', 'success');
                setTimeout(() => viewReport(reportId), 500);
                break;
            case 'error':
                stopStream(); stopGenTimer();
                toast('生成失败: ' + (data.message || '未知错误'), 'error', 5000);
                switchView('create');
                break;
        }
    };

    eventSource.onerror = function() {
        stopStream();
        startPolling(reportId);
    };
}

function stopStream() { if (eventSource) { eventSource.close(); eventSource = null; } }

function updateStreamProgress() {
    const progress = Math.min(92, (streamContent.length / 10000) * 100);
    document.getElementById('progress-fill').style.width = progress + '%';
}

// ===== Polling Fallback =====
function startPolling(reportId) {
    stopPolling();
    pollTimer = setInterval(async () => {
        try {
            const report = await apiCall('/reports/' + reportId);
            if (report.status === 'completed') { stopPolling(); viewReport(reportId); }
            else if (report.status === 'failed') { stopPolling(); toast('生成失败: ' + (report.error_msg || ''), 'error', 5000); switchView('create'); }
        } catch (err) { /* retry */ }
    }, 3000);
}

function stopPolling() { if (pollTimer) { clearInterval(pollTimer); pollTimer = null; } }

function startGenTimer() {
    stopGenTimer();
    genStartTime = Date.now();
    const el = document.getElementById('generating-time');
    updateGenTime(el);
    genTimeTimer = setInterval(() => updateGenTime(el), 1000);
}
function stopGenTimer() { if (genTimeTimer) { clearInterval(genTimeTimer); genTimeTimer = null; } }

function updateGenTime(el) {
    if (!genStartTime) return;
    const s = Math.floor((Date.now() - genStartTime) / 1000);
    el.textContent = s < 60 ? s + '秒' : Math.floor(s/60) + '分' + (s%60) + '秒';
}

// ===== Report List =====
async function loadReports(query) {
    const container = document.getElementById('report-list');
    try {
        const url = query ? '/reports?q=' + encodeURIComponent(query) : '/reports';
        allReports = await apiCall(url);
        applyFilters();
    } catch (err) {
        container.innerHTML = '<div class="empty-state"><p>加载失败: ' + escapeHtml(err.message) + '</p></div>';
    }
}

function applyFilters() {
    const statusFilter = document.getElementById('filter-status').value;
    const sortOrder = document.getElementById('sort-order').value;

    let filtered = [...allReports];

    if (currentTypeFilter) filtered = filtered.filter(r => (r.report_type || 'industry') === currentTypeFilter);
    if (statusFilter) filtered = filtered.filter(r => r.status === statusFilter);

    filtered.sort((a, b) => {
        const da = new Date(a.created_at), db = new Date(b.created_at);
        return sortOrder === 'oldest' ? da - db : db - da;
    });

    renderReportList(filtered);
}

function renderReportList(reports) {
    const container = document.getElementById('report-list');
    const statsEl = document.getElementById('list-stats');

    if (!reports || reports.length === 0) {
        statsEl.textContent = '';
        const isSearch = document.getElementById('search-input').value.trim();
        container.innerHTML = isSearch
            ? '<div class="empty-state"><p>未找到匹配的报告</p></div>'
            : '<div class="empty-state"><div class="empty-icon">&#128196;</div><p>暂无报告</p><p class="empty-sub">点击左侧「生成新报告」创建第一份行业研究</p></div>';
        return;
    }

    statsEl.textContent = `共 ${reports.length} 份报告`;

    container.innerHTML = reports.map(r => `
        <div class="report-card" onclick="viewReport('${r.id}')">
            <div class="report-card-info">
                <div class="report-card-title">${escapeHtml(r.title || r.topic)}</div>
                <div class="report-card-meta">
                    <span>${escapeHtml(r.topic)}</span>
                    ${r.direction ? `<span>${escapeHtml(r.direction)}</span>` : ''}
                    <span class="badge-depth">${reportTypeLabel(r.report_type)}</span>
                        <span class="badge-depth">${depthLabel(r.depth)}</span>
                        ${r.file_count > 0 ? '<span>&#128206; ' + r.file_count + ' 份材料</span>' : ''}
                    <span>${formatTime(r.created_at)}</span>
                </div>
            </div>
            <div class="report-card-actions">
                <span class="badge badge-${r.status}">${statusLabel(r.status)}</span>
                <button class="btn-danger-sm" onclick="event.stopPropagation(); deleteReport('${r.id}')">删除</button>
            </div>
        </div>
    `).join('');
}

// ===== Search =====
function debounceSearch() {
    if (searchTimer) clearTimeout(searchTimer);
    const input = document.getElementById('search-input');
    const clearBtn = document.getElementById('search-clear');
    clearBtn.style.display = input.value ? 'block' : 'none';
    searchTimer = setTimeout(() => {
        loadReports(input.value.trim() || undefined);
    }, 300);
}

function clearSearch() {
    document.getElementById('search-input').value = '';
    document.getElementById('search-clear').style.display = 'none';
    loadReports();
}

// ===== Report Detail =====
async function viewReport(id) {
    try {
        const report = await apiCall('/reports/' + id);

        if (report.status === 'generating' || report.status === 'pending') {
            currentReportId = id;
            document.getElementById('generating-topic').textContent = report.config.topic;
            document.getElementById('progress-fill').style.width = '0%';
            streamContent = '';
            const streamEl = document.getElementById('stream-output');
            streamEl.innerHTML = '<div class="report-body"><div class="stream-placeholder"><div class="stream-placeholder-icon">&#129302;</div><p>AI 正在分析行业数据并撰写报告</p><p class="stream-placeholder-sub">内容将实时显示在此处...</p></div></div>';
            switchView('generating');
            startGenTimer();
            startStream(id);
            return;
        }

        // Meta info
        document.getElementById('detail-meta').innerHTML = `
            <h2>${escapeHtml(report.title || report.config.topic)}</h2>
            <div class="detail-meta-info">
                <span>${escapeHtml(report.config.topic)}</span>
                ${report.config.direction ? '<span>' + escapeHtml(report.config.direction) + '</span>' : ''}
                <span class="badge-depth">${depthLabel(report.config.depth)}</span>
                <span>${formatTime(report.created_at)}</span>
                ${report.completed_at ? '<span>耗时 ' + calcDuration(report.created_at, report.completed_at) + '</span>' : ''}
            </div>
        `;

        // Action buttons
        document.getElementById('detail-actions').innerHTML = `
            <button class="btn-secondary" onclick="copyReport('${id}')">&#128203; 复制</button>
            <button class="btn-secondary" onclick="downloadReport('${id}')">&#128229; 下载</button>
            <button class="btn-secondary" onclick="window.print()">&#128424; 打印</button>
        `;

        const bodyEl = document.getElementById('report-body');
        const tocEl = document.getElementById('report-toc');

        if (report.status === 'failed') {
            bodyEl.innerHTML = `<div class="empty-state"><p>报告生成失败: ${escapeHtml(report.error_msg || '未知错误')}</p></div>`;
            tocEl.innerHTML = '';
        } else {
            bodyEl.innerHTML = renderMarkdown(report.content || '');
            buildTOC(bodyEl, tocEl);
        }

        switchView('detail');
    } catch (err) {
        toast('加载报告失败: ' + err.message, 'error');
    }
}

// ===== Table of Contents =====
function buildTOC(bodyEl, tocEl) {
    const headings = bodyEl.querySelectorAll('h2, h3');
    if (headings.length < 3) { tocEl.innerHTML = ''; return; }

    let html = '<div class="toc-title">目录</div><div class="toc-list">';
    headings.forEach((h, i) => {
        const id = 'section-' + i;
        h.id = id;
        const cls = h.tagName === 'H3' ? ' toc-h3' : '';
        html += `<a class="toc-item${cls}" href="#${id}" onclick="scrollToSection(event, '${id}')">${escapeHtml(h.textContent)}</a>`;
    });
    html += '</div>';
    tocEl.innerHTML = html;
}

function scrollToSection(event, id) {
    event.preventDefault();
    const el = document.getElementById(id);
    if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' });
    // Highlight active TOC item
    document.querySelectorAll('.toc-item').forEach(t => t.classList.remove('active'));
    event.target.classList.add('active');
}

// ===== Actions =====
async function deleteReport(id) {
    if (!confirm('确定要删除这份报告吗？此操作不可撤销。')) return;
    try {
        await apiCall('/reports/' + id, { method: 'DELETE' });
        toast('报告已删除', 'success');
        loadReports();
    } catch (err) { toast('删除失败: ' + err.message, 'error'); }
}

async function copyReport(id) {
    try {
        const report = await apiCall('/reports/' + id);
        await navigator.clipboard.writeText(report.content || '');
        toast('报告内容已复制到剪贴板', 'success');
    } catch (err) { toast('复制失败: ' + err.message, 'error'); }
}

async function downloadReport(id) {
    try {
        const report = await apiCall('/reports/' + id);
        const blob = new Blob([report.content || ''], { type: 'text/markdown;charset=utf-8' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = (report.title || 'report').replace(/[/\\?%*:|"<>]/g, '-') + '.md';
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
        toast('报告已下载', 'success');
    } catch (err) { toast('下载失败: ' + err.message, 'error'); }
}

// ===== Markdown Renderer =====
function renderMarkdown(md) {
    if (!md) return '';
    let html = escapeHtml(md);

    // Headers
    html = html.replace(/^#### (.+)$/gm, '<h4>$1</h4>');
    html = html.replace(/^### (.+)$/gm, '<h3>$1</h3>');
    html = html.replace(/^## (.+)$/gm, '<h2>$1</h2>');
    html = html.replace(/^# (.+)$/gm, '<h1>$1</h1>');

    // Bold + italic
    html = html.replace(/\*\*\*(.+?)\*\*\*/g, '<strong><em>$1</em></strong>');
    html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
    html = html.replace(/(?<!\*)\*(?!\*)(.+?)(?<!\*)\*(?!\*)/g, '<em>$1</em>');

    // Blockquotes
    html = html.replace(/^&gt; (.+)$/gm, '<blockquote>$1</blockquote>');

    // Tables
    html = html.replace(/^\|(.+)\|$/gm, function(match, content) {
        const cells = content.split('|').map(c => c.trim());
        if (cells.every(c => /^[-:]+$/.test(c))) return '<!--sep-->';
        return '<tr>' + cells.map(c => '<td>' + c + '</td>').join('') + '</tr>';
    });
    html = html.replace(/((<tr>.+<\/tr>\n?)+)/g, function(match) {
        let cleaned = match.replace(/<!--sep-->\n?/g, '');
        if (!cleaned.trim()) return '';
        cleaned = cleaned.replace(/<tr>(.+?)<\/tr>/, function(m, inner) {
            return '<thead><tr>' + inner.replace(/<td>/g, '<th>').replace(/<\/td>/g, '</th>') + '</tr></thead><tbody>';
        });
        return '<table>' + cleaned + '</tbody></table>';
    });
    html = html.replace(/<!--sep-->\n?/g, '');

    // Lists
    html = html.replace(/^- (.+)$/gm, '<li>$1</li>');
    html = html.replace(/((<li>.+<\/li>\n?)+)/g, '<ul>$1</ul>');
    html = html.replace(/^\d+\. (.+)$/gm, '<li>$1</li>');

    // HR
    html = html.replace(/^---$/gm, '<hr>');

    // Inline code
    html = html.replace(/`(.+?)`/g, '<code>$1</code>');

    // Paragraphs
    html = html.split('\n\n').map(block => {
        block = block.trim();
        if (!block) return '';
        if (/^<[a-z]/.test(block) || /^<\//.test(block)) return block;
        return '<p>' + block.replace(/\n/g, '<br>') + '</p>';
    }).join('\n');

    return html;
}

// ===== Utilities =====
function escapeHtml(str) {
    const d = document.createElement('div');
    d.textContent = str;
    return d.innerHTML;
}

function statusLabel(s) {
    return { pending: '等待中', generating: '生成中', completed: '已完成', failed: '失败' }[s] || s;
}

function reportTypeLabel(t) {
    return { industry: '行业研究', checklist: '尽调清单', memo: '立项报告', questions: '核心问题' }[t] || t || '行业研究';
}

function depthLabel(d) {
    return { brief: '概览', standard: '标准', deep: '深度' }[d] || d;
}

function formatTime(ts) {
    if (!ts) return '';
    const d = new Date(ts);
    const pad = n => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function calcDuration(start, end) {
    const s = Math.round((new Date(end) - new Date(start)) / 1000);
    if (s < 60) return s + '秒';
    return Math.floor(s/60) + '分' + (s%60) + '秒';
}

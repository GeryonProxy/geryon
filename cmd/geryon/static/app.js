// Geryon Dashboard JavaScript
(function() {
    'use strict';

    // Dashboard state
    const state = {
        currentPage: 'overview',
        stats: null,
        eventSource: null,
        refreshInterval: null,
        qpsHistory: [],        // Time-series QPS data points
        qpsMaxPoints: 60       // Keep last 60 data points (5 min at 5s intervals)
    };

    // DOM element cache
    const elements = {};

    // Initialize dashboard
    function init() {
        cacheElements();
        setupNavigation();
        setupEventListeners();
        loadPage('overview');
        startRealtimeUpdates();
    }

    // Cache DOM elements
    function cacheElements() {
        elements.content = document.getElementById('content');
        elements.pageTitle = document.getElementById('page-title');
        elements.refreshBtn = document.getElementById('refresh-btn');
        elements.reloadConfigBtn = document.getElementById('reload-config-btn');
        elements.connectionStatus = document.getElementById('connection-status');
        elements.statusIndicator = document.querySelector('.status-indicator');
    }

    // Setup navigation
    function setupNavigation() {
        const nav = document.querySelector('nav');
        nav.addEventListener('click', (e) => {
            const link = e.target.closest('.nav-item');
            if (link) {
                e.preventDefault();
                const page = link.getAttribute('data-page');
                setActivePage(page);
                loadPage(page);
            }
        });
    }

    // Setup event listeners
    function setupEventListeners() {
        if (elements.refreshBtn) {
            elements.refreshBtn.addEventListener('click', () => {
                refreshCurrentPage();
            });
        }

        if (elements.reloadConfigBtn) {
            elements.reloadConfigBtn.addEventListener('click', reloadConfig);
        }

        // Handle page visibility changes
        document.addEventListener('visibilitychange', () => {
            if (document.hidden) {
                stopRealtimeUpdates();
            } else {
                startRealtimeUpdates();
                refreshCurrentPage();
            }
        });
    }

    // Set active page in navigation
    function setActivePage(page) {
        document.querySelectorAll('.nav-item').forEach(a => {
            a.classList.toggle('active', a.getAttribute('data-page') === page);
        });
        state.currentPage = page;
        if (elements.pageTitle) {
            elements.pageTitle.textContent = capitalize(page);
        }
    }

    // Load page content
    function loadPage(page) {
        const content = elements.content;
        content.textContent = '';

        const loading = document.createElement('div');
        loading.className = 'loading';
        loading.textContent = 'Loading...';
        content.appendChild(loading);

        switch (page) {
            case 'overview':
                loadOverview(content);
                break;
            case 'pools':
                loadPools(content);
                break;
            case 'backends':
                loadBackends(content);
                break;
            case 'connections':
                loadConnections(content);
                break;
            case 'queries':
                loadQueries(content);
                break;
            case 'cache':
                loadCache(content);
                break;
            case 'cluster':
                loadCluster(content);
                break;
            case 'transactions':
                loadTransactions(content);
                break;
            case 'config':
                loadConfig(content);
                break;
            case 'users':
                loadUsers(content);
                break;
            default:
                loading.textContent = 'Page not found';
        }
    }

    // Refresh current page
    function refreshCurrentPage() {
        loadPage(state.currentPage);
    }

    // Create stat card element
    function createStatCard(title, valueId, icon) {
        const card = document.createElement('div');
        card.className = 'stat-card';

        const iconDiv = document.createElement('div');
        iconDiv.className = 'stat-icon';
        iconDiv.textContent = icon;

        const content = document.createElement('div');
        content.className = 'stat-content';

        const h3 = document.createElement('h3');
        h3.textContent = title;

        const value = document.createElement('div');
        value.className = 'stat-value';
        value.id = valueId;
        value.textContent = '-';

        const change = document.createElement('div');
        change.className = 'stat-change';
        change.textContent = 'Loading...';

        content.appendChild(h3);
        content.appendChild(value);
        content.appendChild(change);
        card.appendChild(iconDiv);
        card.appendChild(content);

        return card;
    }

    // Load overview page
    function loadOverview(container) {
        container.textContent = '';

        // Stats grid
        const statsGrid = document.createElement('div');
        statsGrid.className = 'stats-grid';
        statsGrid.appendChild(createStatCard('Total Connections', 'stat-connections', '🔗'));
        statsGrid.appendChild(createStatCard('Active Pools', 'stat-pools', '🗄️'));
        statsGrid.appendChild(createStatCard('Healthy Backends', 'stat-backends', '✅'));
        statsGrid.appendChild(createStatCard('Queries/sec', 'stat-qps', '⚡'));
        statsGrid.appendChild(createStatCard('Cache Hit Rate', 'stat-cache-hit', '🎯'));
        statsGrid.appendChild(createStatCard('Active Transactions', 'stat-active-tx', '🔄'));
        container.appendChild(statsGrid);

        // Dashboard grid
        const dashboardGrid = document.createElement('div');
        dashboardGrid.className = 'dashboard-grid';

        // Health status card
        const healthCard = document.createElement('div');
        healthCard.className = 'card';
        healthCard.innerHTML = '<h3>Health Status</h3><div id="health-content">Loading...</div>';
        dashboardGrid.appendChild(healthCard);

        // Pool status card
        const poolCard = document.createElement('div');
        poolCard.className = 'card';
        poolCard.innerHTML = '<h3>Pool Status</h3><div id="pool-status-content">Loading...</div>';
        dashboardGrid.appendChild(poolCard);

        container.appendChild(dashboardGrid);

        // QPS time-series chart
        const chartCard = document.createElement('div');
        chartCard.className = 'card';

        const chartTitle = document.createElement('h3');
        chartTitle.textContent = 'Queries/sec (Last 5 Minutes)';
        chartCard.appendChild(chartTitle);

        const canvas = document.createElement('canvas');
        canvas.id = 'qps-chart';
        canvas.style.cssText = 'width: 100%; height: 200px; display: block;';
        canvas.setAttribute('height', '200');
        chartCard.appendChild(canvas);

        container.appendChild(chartCard);

        fetchOverviewData();
    }

    // Fetch overview data
    async function fetchOverviewData() {
        try {
            const [statsRes, healthRes, poolsRes, queriesRes, txRes] = await Promise.all([
                fetch('/api/v1/stats').catch(() => null),
                fetch('/api/v1/health').catch(() => null),
                fetch('/api/v1/pools').catch(() => null),
                fetch('/api/v1/queries').catch(() => null),
                fetch('/api/v1/transactions').catch(() => null)
            ]);

            if (statsRes && statsRes.ok) {
                const stats = await statsRes.json();
                updateStatElement('stat-connections', stats.total_connections);
                updateStatElement('stat-pools', stats.active_pools);
                updateStatElement('stat-qps', stats.queries_per_sec);
            }

            if (healthRes && healthRes.ok) {
                const health = await healthRes.json();
                updateHealthContent(health);
                updateStatElement('stat-backends', health.healthy_backends);
            }

            if (poolsRes && poolsRes.ok) {
                const pools = await poolsRes.json();
                updatePoolStatusContent(pools);
            }

            // Update query stats on overview
            if (queriesRes && queriesRes.ok) {
                const queries = await queriesRes.json();
                const total = queries.total_queries || 0;
                const cached = queries.cached_queries || 0;
                const hitRate = total > 0 ? ((cached / total) * 100).toFixed(1) + '%' : '0%';
                updateStatElement('stat-cache-hit', hitRate);
            }

            // Update transaction stats on overview
            if (txRes && txRes.ok) {
                const tx = await txRes.json();
                updateStatElement('stat-active-tx', tx.active_transactions || 0);
            }
        } catch (err) {
            console.error('Failed to fetch overview data:', err);
        }
    }

    // Update stat element safely
    function updateStatElement(id, value) {
        const el = document.getElementById(id);
        if (el) {
            el.textContent = value !== undefined ? value : '-';
        }
    }

    // Update health content
    function updateHealthContent(health) {
        const container = document.getElementById('health-content');
        if (!container) return;

        container.textContent = '';

        const list = document.createElement('div');
        list.className = 'pool-list';

        if (health.backends && health.backends.length > 0) {
            health.backends.forEach(backend => {
                const item = document.createElement('div');
                item.className = 'pool-item';

                const info = document.createElement('div');
                info.className = 'pool-info';

                const name = document.createElement('div');
                name.className = 'pool-name';
                name.textContent = backend.address || 'Unknown';

                const meta = document.createElement('div');
                meta.className = 'pool-meta';
                meta.textContent = 'Latency: ' + (backend.latency || 'N/A');

                info.appendChild(name);
                info.appendChild(meta);

                const status = document.createElement('span');
                status.className = 'status ' + (backend.status || 'unknown');
                status.textContent = backend.status || 'unknown';

                item.appendChild(info);
                item.appendChild(status);
                list.appendChild(item);
            });
        } else {
            list.textContent = 'No backend data available';
        }

        container.appendChild(list);
    }

    // Update pool status content
    function updatePoolStatusContent(pools) {
        const container = document.getElementById('pool-status-content');
        if (!container) return;

        container.textContent = '';

        if (pools.pools && pools.pools.length > 0) {
            const table = document.createElement('table');
            table.className = 'data-table';

            const thead = document.createElement('thead');
            const headerRow = document.createElement('tr');
            ['Name', 'Body', 'Mode', 'Status'].forEach(text => {
                const th = document.createElement('th');
                th.textContent = text;
                headerRow.appendChild(th);
            });
            thead.appendChild(headerRow);
            table.appendChild(thead);

            const tbody = document.createElement('tbody');
            pools.pools.forEach(pool => {
                const row = document.createElement('tr');

                const nameCell = document.createElement('td');
                nameCell.textContent = pool.name || '-';

                const bodyCell = document.createElement('td');
                bodyCell.textContent = pool.body || '-';

                const modeCell = document.createElement('td');
                modeCell.textContent = pool.mode || '-';

                const statusCell = document.createElement('td');
                const statusSpan = document.createElement('span');
                statusSpan.className = 'status online';
                statusSpan.textContent = 'online';
                statusCell.appendChild(statusSpan);

                row.appendChild(nameCell);
                row.appendChild(bodyCell);
                row.appendChild(modeCell);
                row.appendChild(statusCell);
                tbody.appendChild(row);
            });
            table.appendChild(tbody);
            container.appendChild(table);
        } else {
            container.textContent = 'No pools configured';
        }
    }

    // Load pools page
    function loadPools(container) {
        container.textContent = '';

        const card = document.createElement('div');
        card.className = 'card';

        const title = document.createElement('h3');
        title.textContent = 'Connection Pools';
        card.appendChild(title);

        const content = document.createElement('div');
        content.id = 'pools-content';
        content.textContent = 'Loading pools...';
        card.appendChild(content);

        container.appendChild(card);
        fetchPools();
    }

    // Fetch pools from API
    async function fetchPools() {
        try {
            const response = await fetch('/api/v1/pools');
            if (!response.ok) throw new Error('Failed to fetch pools');
            const data = await response.json();
            updatePoolsContent(data);
        } catch (err) {
            const content = document.getElementById('pools-content');
            if (content) content.textContent = 'Error loading pools: ' + err.message;
        }
    }

    // Update pools content safely using DOM methods
    function updatePoolsContent(data) {
        const container = document.getElementById('pools-content');
        if (!container) return;
        container.textContent = '';

        if (!data.pools || data.pools.length === 0) {
            container.textContent = 'No pools configured';
            return;
        }

        data.pools.forEach(pool => {
            const item = document.createElement('div');
            item.className = 'pool-item';
            item.style.cursor = 'pointer';
            item.onclick = () => showPoolDetail(pool.name);

            const info = document.createElement('div');
            info.className = 'pool-info';

            const name = document.createElement('div');
            name.className = 'pool-name';
            name.textContent = pool.name || 'Unnamed Pool';

            const meta = document.createElement('div');
            meta.className = 'pool-meta';
            meta.textContent = 'Body: ' + (pool.body || 'N/A') + ' | Mode: ' + (pool.mode || 'N/A');

            info.appendChild(name);
            info.appendChild(meta);

            const stats = document.createElement('div');
            stats.className = 'pool-stats';

            const statLabels = [
                { label: 'Active', value: pool.active_connections || 0 },
                { label: 'Idle', value: pool.idle_connections || 0 },
                { label: 'Total', value: pool.total_connections || 0 }
            ];

            statLabels.forEach(stat => {
                const statDiv = document.createElement('div');
                statDiv.className = 'pool-stat';

                const value = document.createElement('div');
                value.className = 'pool-stat-value';
                value.textContent = stat.value;

                const label = document.createElement('div');
                label.className = 'pool-stat-label';
                label.textContent = stat.label;

                statDiv.appendChild(value);
                statDiv.appendChild(label);
                stats.appendChild(statDiv);
            });

            item.appendChild(info);
            item.appendChild(stats);
            container.appendChild(item);
        });
    }

    // Show pool detail modal
    async function showPoolDetail(poolName) {
        try {
            const response = await fetch('/api/v1/pools/' + encodeURIComponent(poolName));
            if (!response.ok) throw new Error('Failed to fetch pool details');
            const pool = await response.json();

            // Create modal
            const modal = document.createElement('div');
            modal.className = 'modal';
            modal.style.cssText = 'position: fixed; top: 0; left: 0; width: 100%; height: 100%; background: rgba(0,0,0,0.7); display: flex; align-items: center; justify-content: center; z-index: 1000;';

            const content = document.createElement('div');
            content.className = 'modal-content';
            content.style.cssText = 'background: var(--bg-secondary); border-radius: 12px; padding: 24px; max-width: 600px; width: 90%; max-height: 80vh; overflow: auto;';

            // Header
            const header = document.createElement('div');
            header.style.cssText = 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px;';

            const title = document.createElement('h2');
            title.textContent = pool.name || 'Pool Details';
            title.style.margin = '0';

            const closeBtn = document.createElement('button');
            closeBtn.textContent = '✕';
            closeBtn.style.cssText = 'background: none; border: none; color: var(--text-primary); font-size: 20px; cursor: pointer;';
            closeBtn.onclick = () => modal.remove();

            header.appendChild(title);
            header.appendChild(closeBtn);
            content.appendChild(header);

            // Connection stats
            const connSection = document.createElement('div');
            connSection.innerHTML = '<h3 style="color: var(--accent); margin-top: 0;">Connection Statistics</h3>';

            const connGrid = document.createElement('div');
            connGrid.style.cssText = 'display: grid; grid-template-columns: repeat(2, 1fr); gap: 12px; margin-bottom: 20px;';

            const connStats = [
                { label: 'Client Connections', value: pool.client_connections || 0 },
                { label: 'Server Connections', value: pool.server_connections || 0 },
                { label: 'Idle Connections', value: pool.idle_connections || 0 },
                { label: 'Active Connections', value: pool.active_connections || 0 },
                { label: 'Waiting Clients', value: pool.waiting_clients || 0 },
                { label: 'Total Queries', value: pool.total_queries || 0 }
            ];

            connStats.forEach(stat => {
                const div = document.createElement('div');
                div.style.cssText = 'background: var(--bg-tertiary); padding: 12px; border-radius: 8px;';
                div.innerHTML = '<div style="color: var(--text-secondary); font-size: 12px;">' + stat.label + '</div>' +
                               '<div style="font-size: 20px; font-weight: 600; margin-top: 4px;">' + stat.value + '</div>';
                connGrid.appendChild(div);
            });

            connSection.appendChild(connGrid);
            content.appendChild(connSection);

            // Prepared Statement Cache
            if (pool.prepared_stmt_cache) {
                const stmtSection = document.createElement('div');
                stmtSection.innerHTML = '<h3 style="color: var(--accent); margin-top: 0;">Prepared Statement Cache</h3>';

                const stmtGrid = document.createElement('div');
                stmtGrid.style.cssText = 'display: grid; grid-template-columns: repeat(2, 1fr); gap: 12px;';

                const stmtStats = [
                    { label: 'Cache Size', value: pool.prepared_stmt_cache.size || 0 },
                    { label: 'Hit Rate', value: (pool.prepared_stmt_cache.hit_rate || 0).toFixed(1) + '%' }
                ];

                stmtStats.forEach(stat => {
                    const div = document.createElement('div');
                    div.style.cssText = 'background: var(--bg-tertiary); padding: 12px; border-radius: 8px;';
                    div.innerHTML = '<div style="color: var(--text-secondary); font-size: 12px;">' + stat.label + '</div>' +
                                   '<div style="font-size: 20px; font-weight: 600; margin-top: 4px;">' + stat.value + '</div>';
                    stmtGrid.appendChild(div);
                });

                stmtSection.appendChild(stmtGrid);
                content.appendChild(stmtSection);
            }

            modal.appendChild(content);
            document.body.appendChild(modal);

            // Close on outside click
            modal.onclick = (e) => {
                if (e.target === modal) modal.remove();
            };

        } catch (err) {
            showNotification('Error loading pool details: ' + err.message, 'error');
        }
    }

    // Load backends page
    function loadBackends(container) {
        container.textContent = '';

        const card = document.createElement('div');
        card.className = 'card';

        const header = document.createElement('div');
        header.style.cssText = 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px;';

        const title = document.createElement('h3');
        title.textContent = 'Backend Servers';
        header.appendChild(title);

        const addBtn = document.createElement('button');
        addBtn.className = 'btn btn-primary';
        addBtn.textContent = '+ Add Backend';
        addBtn.addEventListener('click', showAddBackendModal);
        header.appendChild(addBtn);

        card.appendChild(header);

        const content = document.createElement('div');
        content.id = 'backends-content';
        content.textContent = 'Loading backends...';
        card.appendChild(content);

        container.appendChild(card);
        fetchBackends();
    }

    // Fetch backends from API
    async function fetchBackends() {
        try {
            // Fetch all pools first to get per-pool backends
            const poolsResp = await fetch('/api/v1/pools');
            if (!poolsResp.ok) throw new Error('Failed to fetch pools');
            const poolsData = await poolsResp.json();

            const result = { pools: poolsData.pools || [], perPoolBackends: {} };

            // Fetch backends for each pool
            for (const pool of result.pools) {
                try {
                    const resp = await fetch('/api/v1/pools/' + encodeURIComponent(pool.name) + '/backends');
                    if (resp.ok) {
                        result.perPoolBackends[pool.name] = await resp.json();
                    }
                } catch (e) {
                    // Pool may not support the backends endpoint
                }
            }

            updateBackendsContent(result);
        } catch (err) {
            const content = document.getElementById('backends-content');
            if (content) content.textContent = 'Error loading backends: ' + err.message;
        }
    }

    // Update backends content showing per-pool backends
    function updateBackendsContent(data) {
        const container = document.getElementById('backends-content');
        if (!container) return;
        container.textContent = '';

        if (!data.pools || data.pools.length === 0) {
            container.textContent = 'No pools configured';
            return;
        }

        data.pools.forEach(pool => {
            // Pool header
            const poolHeader = document.createElement('h4');
            poolHeader.style.cssText = 'margin: 16px 0 8px 0; color: var(--text-primary);';
            poolHeader.textContent = pool.name + ' (' + (pool.body || 'N/A') + ')';
            container.appendChild(poolHeader);

            const backends = (data.perPoolBackends[pool.name] && data.perPoolBackends[pool.name].backends) || [];

            if (backends.length === 0) {
                const empty = document.createElement('div');
                empty.style.cssText = 'color: var(--text-muted); font-size: 14px; margin-bottom: 12px;';
                empty.textContent = 'No backends for this pool';
                container.appendChild(empty);
                return;
            }

            const table = document.createElement('table');
            table.className = 'data-table';
            table.style.cssText = 'margin-bottom: 16px;';

            const thead = document.createElement('thead');
            const headerRow = document.createElement('tr');
            ['Address', 'Role', 'Weight', 'Status', 'Connections', 'Actions'].forEach(text => {
                const th = document.createElement('th');
                th.textContent = text;
                headerRow.appendChild(th);
            });
            thead.appendChild(headerRow);
            table.appendChild(thead);

            const tbody = document.createElement('tbody');
            backends.forEach(backend => {
                const row = document.createElement('tr');

                const addrCell = document.createElement('td');
                addrCell.textContent = backend.address || '-';
                row.appendChild(addrCell);

                const roleCell = document.createElement('td');
                const role = document.createElement('span');
                role.className = 'badge badge-' + (backend.role === 'primary' ? 'primary' : 'secondary');
                role.textContent = backend.role || 'unknown';
                roleCell.appendChild(role);
                row.appendChild(roleCell);

                const weightCell = document.createElement('td');
                weightCell.textContent = backend.weight || 1;
                row.appendChild(weightCell);

                const statusCell = document.createElement('td');
                const status = document.createElement('span');
                status.className = 'status ' + (backend.healthy ? 'up' : 'down');
                status.textContent = backend.healthy ? 'up' : 'down';
                statusCell.appendChild(status);
                row.appendChild(statusCell);

                const connCell = document.createElement('td');
                connCell.textContent = backend.connections || 0;
                row.appendChild(connCell);

                const actionsCell = document.createElement('td');
                const removeBtn = document.createElement('button');
                removeBtn.className = 'btn btn-secondary';
                removeBtn.textContent = 'Remove';
                removeBtn.style.cssText = 'font-size: 12px; padding: 4px 8px;';
                removeBtn.addEventListener('click', () => removeBackend(pool.name, backend.address));
                actionsCell.appendChild(removeBtn);
                row.appendChild(actionsCell);

                tbody.appendChild(row);
            });
            table.appendChild(tbody);
            container.appendChild(table);
        });
    }

    // Show add backend modal
    function showAddBackendModal() {
        // Fetch pools for dropdown
        fetch('/api/v1/pools')
            .then(r => r.json())
            .then(data => {
                const pools = data.pools || [];
                if (pools.length === 0) {
                    showNotification('No pools configured', 'error');
                    return;
                }
                buildBackendModal(pools);
            })
            .catch(() => showNotification('Failed to load pools', 'error'));
    }

    function buildBackendModal(pools) {
        const overlay = document.createElement('div');
        overlay.style.cssText = 'position: fixed; top: 0; left: 0; width: 100%; height: 100%; ' +
            'background: rgba(0,0,0,0.5); display: flex; align-items: center; justify-content: center; z-index: 1000;';

        const modal = document.createElement('div');
        modal.style.cssText = 'background: var(--card-bg); border-radius: 12px; padding: 24px; ' +
            'width: 400px; max-width: 90vw; box-shadow: 0 8px 32px rgba(0,0,0,0.3);';

        const title = document.createElement('h3');
        title.textContent = 'Add Backend';
        title.style.cssText = 'margin: 0 0 20px 0;';
        modal.appendChild(title);

        // Pool selector
        const poolLabel = document.createElement('label');
        poolLabel.textContent = 'Pool';
        poolLabel.style.cssText = 'display: block; margin-bottom: 4px; font-weight: 500; font-size: 14px;';
        modal.appendChild(poolLabel);

        const poolSelect = document.createElement('select');
        poolSelect.style.cssText = 'width: 100%; padding: 8px; border-radius: 6px; border: 1px solid var(--border); ' +
            'background: var(--input-bg); color: var(--text); margin-bottom: 12px;';
        pools.forEach(pool => {
            const opt = document.createElement('option');
            opt.value = pool.name;
            opt.textContent = pool.name + ' (' + pool.body + ')';
            poolSelect.appendChild(opt);
        });
        modal.appendChild(poolSelect);

        const fields = [
            { id: 'backend-host', label: 'Host', placeholder: 'db.internal' },
            { id: 'backend-port', label: 'Port', placeholder: '5432', type: 'number' },
            { id: 'backend-database', label: 'Database', placeholder: 'myapp' },
        ];

        fields.forEach(f => {
            const label = document.createElement('label');
            label.textContent = f.label;
            label.style.cssText = 'display: block; margin-bottom: 4px; font-weight: 500; font-size: 14px;';
            modal.appendChild(label);

            const input = document.createElement('input');
            input.id = f.id;
            input.placeholder = f.placeholder;
            input.type = f.type || 'text';
            input.style.cssText = 'width: 100%; padding: 8px; border-radius: 6px; border: 1px solid var(--border); ' +
                'background: var(--input-bg); color: var(--text); margin-bottom: 12px;';
            modal.appendChild(input);
        });

        // Role selector
        const roleLabel = document.createElement('label');
        roleLabel.textContent = 'Role';
        roleLabel.style.cssText = 'display: block; margin-bottom: 4px; font-weight: 500; font-size: 14px;';
        modal.appendChild(roleLabel);

        const roleSelect = document.createElement('select');
        roleSelect.id = 'backend-role';
        roleSelect.style.cssText = 'width: 100%; padding: 8px; border-radius: 6px; border: 1px solid var(--border); ' +
            'background: var(--input-bg); color: var(--text); margin-bottom: 12px;';
        ['primary', 'replica'].forEach(r => {
            const opt = document.createElement('option');
            opt.value = r;
            opt.textContent = r;
            roleSelect.appendChild(opt);
        });
        modal.appendChild(roleSelect);

        const buttons = document.createElement('div');
        buttons.style.cssText = 'display: flex; gap: 8px; justify-content: flex-end; margin-top: 16px;';

        const cancelBtn = document.createElement('button');
        cancelBtn.className = 'btn btn-secondary';
        cancelBtn.textContent = 'Cancel';
        cancelBtn.addEventListener('click', () => overlay.remove());
        buttons.appendChild(cancelBtn);

        const submitBtn = document.createElement('button');
        submitBtn.className = 'btn btn-primary';
        submitBtn.textContent = 'Add Backend';
        submitBtn.addEventListener('click', () => createBackend(overlay));
        buttons.appendChild(submitBtn);

        modal.appendChild(buttons);
        overlay.appendChild(modal);
        document.body.appendChild(overlay);
    }

    // Create new backend
    async function createBackend(overlay) {
        const poolName = document.getElementById('backend-host').closest('div, form')
            ? overlay.querySelector('select').value
            : '';
        // Re-get the pool name from the select
        const selects = overlay.querySelectorAll('select');
        const poolSelect = selects[0];
        const hostInput = document.getElementById('backend-host');
        const portInput = document.getElementById('backend-port');
        const dbInput = document.getElementById('backend-database');
        const roleSelect = document.getElementById('backend-role');

        if (!hostInput.value || !portInput.value) {
            showNotification('Host and port are required', 'error');
            return;
        }

        const body = {
            host: hostInput.value,
            port: parseInt(portInput.value),
            role: roleSelect.value,
            weight: 1,
            database: dbInput.value
        };

        try {
            const resp = await fetch('/api/v1/pools/' + encodeURIComponent(poolSelect.value) + '/backends', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
                body: JSON.stringify(body)
            });
            if (!resp.ok) {
                const err = await resp.json().catch(() => ({ error: 'Request failed' }));
                throw new Error(err.error || 'Add backend failed');
            }
            showNotification('Backend added', 'success');
            overlay.remove();
            fetchBackends();
        } catch (err) {
            showNotification('Failed to add backend: ' + err.message, 'error');
        }
    }

    // Remove backend from pool
    async function removeBackend(poolName, address) {
        if (!confirm('Remove backend ' + address + ' from pool ' + poolName + '?')) return;
        try {
            const resp = await fetch('/api/v1/pools/' + encodeURIComponent(poolName) + '/backends?address=' + encodeURIComponent(address), {
                method: 'DELETE',
                headers: { 'X-Requested-With': 'XMLHttpRequest' }
            });
            if (!resp.ok) throw new Error('Remove backend failed');
            showNotification('Backend removed', 'success');
            fetchBackends();
        } catch (err) {
            showNotification('Failed to remove backend: ' + err.message, 'error');
        }
    }

    // Load connections page
    function loadConnections(container) {
        container.textContent = '';

        const card = document.createElement('div');
        card.className = 'card';

        const title = document.createElement('h3');
        title.textContent = 'Active Connections';
        card.appendChild(title);

        const content = document.createElement('div');
        content.id = 'connections-content';
        content.textContent = 'Loading connections...';
        card.appendChild(content);

        container.appendChild(card);
        fetchConnections();
    }

    // Fetch connections from API
    async function fetchConnections() {
        try {
            const response = await fetch('/api/v1/connections');
            if (!response.ok) throw new Error('Failed to fetch connections');
            const data = await response.json();
            updateConnectionsContent(data);
        } catch (err) {
            const content = document.getElementById('connections-content');
            if (content) content.textContent = 'Error loading connections: ' + err.message;
        }
    }

    // Update connections content
    function updateConnectionsContent(data) {
        const container = document.getElementById('connections-content');
        if (!container) return;
        container.textContent = '';

        if (!data.connections || data.connections.length === 0) {
            container.textContent = 'No active connections';
            return;
        }

        const table = document.createElement('table');
        table.className = 'data-table';

        const thead = document.createElement('thead');
        const headerRow = document.createElement('tr');
        ['Pool', 'Client Conns', 'Server Conns', 'Idle', 'Active', 'Waiting'].forEach(text => {
            const th = document.createElement('th');
            th.textContent = text;
            headerRow.appendChild(th);
        });
        thead.appendChild(headerRow);
        table.appendChild(thead);

        const tbody = document.createElement('tbody');
        data.connections.forEach(conn => {
            if (!conn.pool_name) return;
            const row = document.createElement('tr');
            [conn.pool_name, conn.client_connections || 0, conn.server_connections || 0, conn.idle_connections || 0, conn.active_connections || 0, conn.waiting_clients || 0].forEach(val => {
                const td = document.createElement('td');
                td.textContent = val;
                row.appendChild(td);
            });
            tbody.appendChild(row);
        });
        table.appendChild(tbody);
        container.appendChild(table);
    }

    // Load queries page
    function loadQueries(container) {
        container.textContent = '';

        // Stats grid for queries
        const statsGrid = document.createElement('div');
        statsGrid.className = 'stats-grid';
        statsGrid.appendChild(createStatCard('Total Queries', 'stat-total-queries', '📊'));
        statsGrid.appendChild(createStatCard('Slow Queries', 'stat-slow-queries', '🐢'));
        statsGrid.appendChild(createStatCard('Cached Queries', 'stat-cached-queries', '⚡'));
        statsGrid.appendChild(createStatCard('Cache Hit Rate', 'stat-cache-hit', '🎯'));
        container.appendChild(statsGrid);

        // Tabs container
        const tabsContainer = document.createElement('div');
        tabsContainer.className = 'tabs';

        // Tab buttons
        const tabButtons = document.createElement('div');
        tabButtons.className = 'tab-buttons';

        const overviewBtn = document.createElement('button');
        overviewBtn.className = 'tab-btn active';
        overviewBtn.textContent = 'Overview';
        overviewBtn.onclick = () => switchTab('queries-overview');

        const slowBtn = document.createElement('button');
        slowBtn.className = 'tab-btn';
        slowBtn.textContent = 'Slow Queries';
        slowBtn.onclick = () => switchTab('queries-slow');

        tabButtons.appendChild(overviewBtn);
        tabButtons.appendChild(slowBtn);
        tabsContainer.appendChild(tabButtons);

        // Tab content - Overview
        const overviewTab = document.createElement('div');
        overviewTab.id = 'tab-queries-overview';
        overviewTab.className = 'tab-content active';

        const overviewCard = document.createElement('div');
        overviewCard.className = 'card';

        const overviewTitle = document.createElement('h3');
        overviewTitle.textContent = 'Query Statistics';
        overviewCard.appendChild(overviewTitle);

        const overviewContent = document.createElement('div');
        overviewContent.id = 'queries-content';
        overviewContent.textContent = 'Loading query statistics...';
        overviewCard.appendChild(overviewContent);

        overviewTab.appendChild(overviewCard);
        tabsContainer.appendChild(overviewTab);

        // Tab content - Slow Queries
        const slowTab = document.createElement('div');
        slowTab.id = 'tab-queries-slow';
        slowTab.className = 'tab-content';

        const slowCard = document.createElement('div');
        slowCard.className = 'card';

        const slowTitle = document.createElement('h3');
        slowTitle.textContent = 'Slow Queries';
        slowCard.appendChild(slowTitle);

        const slowContent = document.createElement('div');
        slowContent.id = 'slow-queries-content';
        slowContent.textContent = 'Loading slow queries...';
        slowCard.appendChild(slowContent);

        slowTab.appendChild(slowCard);
        tabsContainer.appendChild(slowTab);

        container.appendChild(tabsContainer);

        // Add tab styles
        addTabStyles();

        fetchQueries();
        fetchSlowQueries();
    }

    // Switch tab
    function switchTab(tabId) {
        // Hide all tabs
        document.querySelectorAll('.tab-content').forEach(tab => {
            tab.classList.remove('active');
        });
        document.querySelectorAll('.tab-btn').forEach(btn => {
            btn.classList.remove('active');
        });

        // Show selected tab
        const selectedTab = document.getElementById('tab-' + tabId);
        if (selectedTab) {
            selectedTab.classList.add('active');
        }

        // Update button state
        event.target.classList.add('active');
    }

    // Add tab styles
    function addTabStyles() {
        if (document.getElementById('tab-styles')) return;

        const style = document.createElement('style');
        style.id = 'tab-styles';
        style.textContent = `
            .tabs { margin-top: 20px; }
            .tab-buttons { display: flex; gap: 8px; margin-bottom: 16px; border-bottom: 1px solid var(--border); }
            .tab-btn { background: none; border: none; padding: 12px 24px; cursor: pointer; color: var(--text-secondary); font-size: 14px; border-bottom: 2px solid transparent; transition: all 0.2s; }
            .tab-btn:hover { color: var(--text-primary); }
            .tab-btn.active { color: var(--accent); border-bottom-color: var(--accent); }
            .tab-content { display: none; }
            .tab-content.active { display: block; }
        `;
        document.head.appendChild(style);
    }

    // Fetch slow queries from API
    async function fetchSlowQueries() {
        try {
            const response = await fetch('/api/v1/queries/slow?limit=50');
            if (!response.ok) throw new Error('Failed to fetch slow queries');
            const data = await response.json();
            updateSlowQueriesContent(data);
        } catch (err) {
            const content = document.getElementById('slow-queries-content');
            if (content) content.textContent = 'Error loading slow queries: ' + err.message;
        }
    }

    // Update slow queries content
    function updateSlowQueriesContent(data) {
        const container = document.getElementById('slow-queries-content');
        if (!container) return;
        container.textContent = '';

        if (!data.slow_queries || data.slow_queries.length === 0) {
            container.textContent = 'No slow queries found';
            return;
        }

        const table = document.createElement('table');
        table.className = 'data-table';

        const thead = document.createElement('thead');
        const headerRow = document.createElement('tr');
        ['Time', 'Duration', 'Query', 'Pool', 'Client'].forEach(text => {
            const th = document.createElement('th');
            th.textContent = text;
            headerRow.appendChild(th);
        });
        thead.appendChild(headerRow);
        table.appendChild(thead);

        const tbody = document.createElement('tbody');
        data.slow_queries.forEach(query => {
            const row = document.createElement('tr');

            const timeCell = document.createElement('td');
            timeCell.textContent = query.timestamp ? new Date(query.timestamp).toLocaleTimeString() : '-';

            const durationCell = document.createElement('td');
            durationCell.textContent = (query.duration_ms || 0) + ' ms';
            durationCell.style.color = 'var(--error)';

            const queryCell = document.createElement('td');
            queryCell.textContent = (query.query || '-').substring(0, 100) + ((query.query || '').length > 100 ? '...' : '');
            queryCell.style.maxWidth = '400px';
            queryCell.style.overflow = 'hidden';
            queryCell.style.textOverflow = 'ellipsis';

            const poolCell = document.createElement('td');
            poolCell.textContent = query.pool || '-';

            const clientCell = document.createElement('td');
            clientCell.textContent = query.client_addr || '-';

            row.appendChild(timeCell);
            row.appendChild(durationCell);
            row.appendChild(queryCell);
            row.appendChild(poolCell);
            row.appendChild(clientCell);
            tbody.appendChild(row);
        });

        table.appendChild(tbody);
        container.appendChild(table);
    }

    // Fetch queries from API
    async function fetchQueries() {
        try {
            const response = await fetch('/api/v1/queries');
            if (!response.ok) throw new Error('Failed to fetch queries');
            const data = await response.json();
            updateQueriesContent(data);
        } catch (err) {
            const content = document.getElementById('queries-content');
            if (content) content.textContent = 'Error loading queries: ' + err.message;
        }
    }

    // Update queries content
    function updateQueriesContent(data) {
        // Update stat cards
        updateStatElement('stat-total-queries', data.total_queries || 0);
        updateStatElement('stat-slow-queries', data.slow_queries || 0);
        updateStatElement('stat-cached-queries', data.cached_queries || 0);

        // Calculate cache hit rate
        const total = data.total_queries || 0;
        const cached = data.cached_queries || 0;
        const hitRate = total > 0 ? ((cached / total) * 100).toFixed(1) + '%' : '0%';
        updateStatElement('stat-cache-hit', hitRate);

        // Update content
        const container = document.getElementById('queries-content');
        if (!container) return;
        container.textContent = '';

        const table = document.createElement('table');
        table.className = 'data-table';

        const thead = document.createElement('thead');
        const headerRow = document.createElement('tr');
        ['Metric', 'Value'].forEach(text => {
            const th = document.createElement('th');
            th.textContent = text;
            headerRow.appendChild(th);
        });
        thead.appendChild(headerRow);
        table.appendChild(thead);

        const tbody = document.createElement('tbody');
        const metrics = [
            { label: 'Total Queries', value: data.total_queries || 0 },
            { label: 'Slow Queries', value: data.slow_queries || 0 },
            { label: 'Cached Queries', value: data.cached_queries || 0 },
            { label: 'Cache Hit Rate', value: hitRate }
        ];

        metrics.forEach(metric => {
            const row = document.createElement('tr');

            const labelCell = document.createElement('td');
            labelCell.textContent = metric.label;

            const valueCell = document.createElement('td');
            valueCell.textContent = metric.value;

            row.appendChild(labelCell);
            row.appendChild(valueCell);
            tbody.appendChild(row);
        });

        table.appendChild(tbody);
        container.appendChild(table);
    }

    // Load cache page
    function loadCache(container) {
        container.textContent = '';

        const card = document.createElement('div');
        card.className = 'card';

        const title = document.createElement('h3');
        title.textContent = 'Query Cache';
        card.appendChild(title);

        const content = document.createElement('div');
        content.id = 'cache-content';
        content.textContent = 'Loading cache stats...';
        card.appendChild(content);

        container.appendChild(card);
        fetchCache();
    }

    async function fetchCache() {
        try {
            const resp = await fetch('/api/v1/stats');
            if (!resp.ok) throw new Error('Failed to fetch cache stats');
            const data = await resp.json();
            updateCacheContent(data);
        } catch (err) {
            const content = document.getElementById('cache-content');
            if (content) content.textContent = 'Error: ' + err.message;
        }
    }

    function updateCacheContent(data) {
        const container = document.getElementById('cache-content');
        if (!container) return;
        container.textContent = '';

        const grid = document.createElement('div');
        grid.style.cssText = 'display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 16px; margin-bottom: 20px;';

        const stats = [
            { label: 'Cache Hit Rate', value: data.cache_hit_rate ? data.cache_hit_rate.toFixed(1) + '%' : '0%' },
            { label: 'Cached Entries', value: data.cached_queries || 0 },
            { label: 'Cache Hits', value: formatNumber(data.cache_hits || 0) },
            { label: 'Cache Misses', value: formatNumber(data.cache_misses || 0) },
        ];

        stats.forEach(s => {
            const item = document.createElement('div');
            item.style.cssText = 'background: var(--bg); border-radius: 8px; padding: 16px; text-align: center;';

            const val = document.createElement('div');
            val.style.cssText = 'font-size: 24px; font-weight: bold; color: var(--success);';
            val.textContent = s.value;
            item.appendChild(val);

            const lbl = document.createElement('div');
            lbl.style.cssText = 'font-size: 12px; color: var(--text-muted); margin-top: 4px;';
            lbl.textContent = s.label;
            item.appendChild(lbl);

            grid.appendChild(item);
        });

        container.appendChild(grid);

        // Per-pool cache info
        const poolsCard = document.createElement('div');
        poolsCard.className = 'card';

        const poolsTitle = document.createElement('h4');
        poolsTitle.textContent = 'Per-Pool Cache Stats';
        poolsTitle.style.cssText = 'margin: 0 0 12px 0;';
        poolsCard.appendChild(poolsTitle);

        const poolsContent = document.createElement('div');
        poolsContent.id = 'cache-pools-content';
        poolsContent.textContent = 'Loading...';
        poolsCard.appendChild(poolsContent);
        container.appendChild(poolsCard);

        fetch('/api/v1/pools')
            .then(r => r.json())
            .then(poolsData => {
                poolsContent.textContent = '';
                const pools = poolsData.pools || [];
                if (pools.length === 0) {
                    poolsContent.textContent = 'No pools configured';
                    return;
                }
                pools.forEach(pool => {
                    const row = document.createElement('div');
                    row.style.cssText = 'display: flex; justify-content: space-between; padding: 8px 0; border-bottom: 1px solid var(--border);';

                    const name = document.createElement('span');
                    name.textContent = pool.name;
                    row.appendChild(name);

                    const meta = document.createElement('span');
                    meta.style.cssText = 'color: var(--text-muted);';
                    meta.textContent = 'Entries: ' + (pool.query_cache_entries || 0) + ' | Hit Rate: ' + ((pool.query_cache_hit_rate || 0) * 100).toFixed(1) + '%';
                    row.appendChild(meta);

                    poolsContent.appendChild(row);
                });
            });
    }

    // Load cluster page
    function loadCluster(container) {
        container.textContent = '';

        const card = document.createElement('div');
        card.className = 'card';

        const title = document.createElement('h3');
        title.textContent = 'Cluster Status';
        card.appendChild(title);

        const content = document.createElement('div');
        content.id = 'cluster-content';
        content.textContent = 'Loading cluster info...';
        card.appendChild(content);

        container.appendChild(card);
        fetchCluster();
    }

    async function fetchCluster() {
        try {
            // Try cluster stats endpoint; fall back to info message
            const resp = await fetch('/api/v1/cluster');
            if (!resp.ok) {
                updateClusterInfo({ status: 'disabled', message: 'Clustering is not enabled for this node.' });
                return;
            }
            const data = await resp.json();
            updateClusterInfo(data);
        } catch (err) {
            updateClusterInfo({ status: 'disabled', message: 'Cluster status unavailable: ' + err.message });
        }
    }

    function updateClusterInfo(data) {
        const container = document.getElementById('cluster-content');
        if (!container) return;
        container.textContent = '';

        if (data.status === 'disabled' || data.status === 'unavailable') {
            const info = document.createElement('div');
            info.style.cssText = 'padding: 20px; text-align: center; color: var(--text-muted);';
            info.textContent = data.message || 'Clustering is not enabled.';
            container.appendChild(info);
            return;
        }

        // Leader info
        const leader = document.createElement('div');
        leader.style.cssText = 'display: flex; justify-content: space-between; padding: 12px 0; border-bottom: 1px solid var(--border);';

        const leaderLabel = document.createElement('span');
        leaderLabel.textContent = 'Leader';
        leader.appendChild(leaderLabel);

        const leaderVal = document.createElement('span');
        leaderVal.style.cssText = 'color: var(--success); font-weight: bold;';
        leaderVal.textContent = data.leader || 'unknown';
        leader.appendChild(leaderVal);
        container.appendChild(leader);

        // Node count
        const nodes = document.createElement('div');
        nodes.style.cssText = 'display: flex; justify-content: space-between; padding: 12px 0; border-bottom: 1px solid var(--border);';

        const nodesLabel = document.createElement('span');
        nodesLabel.textContent = 'Nodes';
        nodes.appendChild(nodesLabel);

        const nodesVal = document.createElement('span');
        nodesVal.textContent = (data.nodes || []).length + ' total';
        nodes.appendChild(nodesVal);
        container.appendChild(nodes);

        // Node list
        if (data.nodes && data.nodes.length > 0) {
            const table = document.createElement('table');
            table.className = 'data-table';
            table.style.cssText = 'margin-top: 16px;';

            const thead = document.createElement('thead');
            const headerRow = document.createElement('tr');
            ['Node ID', 'Role', 'Status', 'Last Seen'].forEach(text => {
                const th = document.createElement('th');
                th.textContent = text;
                headerRow.appendChild(th);
            });
            thead.appendChild(headerRow);
            table.appendChild(thead);

            const tbody = document.createElement('tbody');
            data.nodes.forEach(node => {
                const row = document.createElement('tr');

                const idCell = document.createElement('td');
                idCell.textContent = node.id || '-';
                row.appendChild(idCell);

                const roleCell = document.createElement('td');
                const isLeader = node.id === data.leader;
                const badge = document.createElement('span');
                badge.className = 'badge badge-' + (isLeader ? 'primary' : 'secondary');
                badge.textContent = isLeader ? 'leader' : 'follower';
                roleCell.appendChild(badge);
                row.appendChild(roleCell);

                const statusCell = document.createElement('td');
                const status = document.createElement('span');
                status.className = 'status ' + (node.healthy ? 'up' : 'down');
                status.textContent = node.healthy ? 'healthy' : 'unreachable';
                statusCell.appendChild(status);
                row.appendChild(statusCell);

                const lastSeen = document.createElement('td');
                lastSeen.textContent = node.last_seen || '-';
                row.appendChild(lastSeen);

                tbody.appendChild(row);
            });
            table.appendChild(tbody);
            container.appendChild(table);
        }
    }

    // Draw QPS time-series chart using canvas
    function drawQPSChart() {
        const canvas = document.getElementById('qps-chart');
        if (!canvas) return;
        const ctx = canvas.getContext('2d');
        const rect = canvas.getBoundingClientRect();
        canvas.width = rect.width * 2;
        canvas.height = 400;
        ctx.scale(2, 2);

        const w = rect.width;
        const h = 200;
        const pad = { top: 20, right: 16, bottom: 28, left: 48 };
        const plotW = w - pad.left - pad.right;
        const plotH = h - pad.top - pad.bottom;

        const data = state.qpsHistory;
        if (data.length < 2) return;

        const maxVal = Math.max(...data.map(d => d.value), 1);

        // Background
        ctx.fillStyle = '#1a1a2e';
        ctx.fillRect(0, 0, w, h);

        // Grid lines
        ctx.strokeStyle = '#2a2a4a';
        ctx.lineWidth = 0.5;
        for (let i = 0; i <= 4; i++) {
            const y = pad.top + (plotH / 4) * i;
            ctx.beginPath();
            ctx.moveTo(pad.left, y);
            ctx.lineTo(w - pad.right, y);
            ctx.stroke();

            ctx.fillStyle = '#666';
            ctx.font = '10px monospace';
            ctx.textAlign = 'right';
            ctx.fillText(Math.round(maxVal - (maxVal / 4) * i), pad.left - 6, y + 3);
        }

        // Time labels
        const first = data[0].time;
        const last = data[data.length - 1].time;
        ctx.fillStyle = '#666';
        ctx.font = '9px monospace';
        ctx.textAlign = 'center';
        ['now', '-1m', '-2m', '-3m', '-4m', '-5m'].forEach((label, i) => {
            const x = pad.left + (plotW / 5) * i;
            ctx.fillText(label, x, h - 6);
        });

        // Line
        ctx.beginPath();
        ctx.strokeStyle = '#00d4ff';
        ctx.lineWidth = 1.5;
        ctx.lineJoin = 'round';
        data.forEach((d, i) => {
            const x = pad.left + (i / (data.length - 1)) * plotW;
            const y = pad.top + plotH - (d.value / maxVal) * plotH;
            if (i === 0) ctx.moveTo(x, y);
            else ctx.lineTo(x, y);
        });
        ctx.stroke();

        // Fill under line
        const lastX = pad.left + plotW;
        const lastY = pad.top + plotH - (data[data.length - 1].value / maxVal) * plotH;
        ctx.lineTo(lastX, pad.top + plotH);
        ctx.lineTo(pad.left, pad.top + plotH);
        ctx.closePath();
        const grad = ctx.createLinearGradient(0, pad.top, 0, pad.top + plotH);
        grad.addColorStop(0, 'rgba(0, 212, 255, 0.25)');
        grad.addColorStop(1, 'rgba(0, 212, 255, 0.02)');
        ctx.fillStyle = grad;
        ctx.fill();

        // Current value indicator
        ctx.beginPath();
        ctx.arc(lastX, lastY, 3, 0, Math.PI * 2);
        ctx.fillStyle = '#00d4ff';
        ctx.fill();
    }

    // Format large numbers with commas
    function formatNumber(n) {
        if (typeof n === 'number') return n.toLocaleString();
        return String(n);
    }

    // Load transactions page
    function loadTransactions(container) {
        container.textContent = '';

        // Stats grid for transactions
        const statsGrid = document.createElement('div');
        statsGrid.className = 'stats-grid';
        statsGrid.appendChild(createStatCard('Active Transactions', 'stat-active-tx', '🔄'));
        statsGrid.appendChild(createStatCard('Total Transactions', 'stat-total-tx', '📈'));
        statsGrid.appendChild(createStatCard('Aborted Transactions', 'stat-aborted-tx', '❌'));
        statsGrid.appendChild(createStatCard('Success Rate', 'stat-tx-success', '✅'));
        container.appendChild(statsGrid);

        // Transaction details card
        const card = document.createElement('div');
        card.className = 'card';

        const title = document.createElement('h3');
        title.textContent = 'Transaction Statistics';
        card.appendChild(title);

        const content = document.createElement('div');
        content.id = 'transactions-content';
        content.textContent = 'Loading transaction statistics...';
        card.appendChild(content);

        container.appendChild(card);
        fetchTransactions();
    }

    // Fetch transactions from API
    async function fetchTransactions() {
        try {
            const response = await fetch('/api/v1/transactions');
            if (!response.ok) throw new Error('Failed to fetch transactions');
            const data = await response.json();
            updateTransactionsContent(data);
        } catch (err) {
            const content = document.getElementById('transactions-content');
            if (content) content.textContent = 'Error loading transactions: ' + err.message;
        }
    }

    // Update transactions content
    function updateTransactionsContent(data) {
        // Update stat cards
        updateStatElement('stat-active-tx', data.active_transactions || 0);
        updateStatElement('stat-total-tx', data.total_transactions || 0);
        updateStatElement('stat-aborted-tx', data.aborted_count || 0);

        // Calculate success rate
        const total = data.total_transactions || 0;
        const aborted = data.aborted_count || 0;
        const successRate = total > 0 ? (((total - aborted) / total) * 100).toFixed(1) + '%' : '100%';
        updateStatElement('stat-tx-success', successRate);

        // Update content
        const container = document.getElementById('transactions-content');
        if (!container) return;
        container.textContent = '';

        const table = document.createElement('table');
        table.className = 'data-table';

        const thead = document.createElement('thead');
        const headerRow = document.createElement('tr');
        ['Metric', 'Value'].forEach(text => {
            const th = document.createElement('th');
            th.textContent = text;
            headerRow.appendChild(th);
        });
        thead.appendChild(headerRow);
        table.appendChild(thead);

        const tbody = document.createElement('tbody');
        const metrics = [
            { label: 'Active Transactions', value: data.active_transactions || 0 },
            { label: 'Total Transactions', value: data.total_transactions || 0 },
            { label: 'Aborted Transactions', value: data.aborted_count || 0 },
            { label: 'Success Rate', value: successRate }
        ];

        metrics.forEach(metric => {
            const row = document.createElement('tr');

            const labelCell = document.createElement('td');
            labelCell.textContent = metric.label;

            const valueCell = document.createElement('td');
            valueCell.textContent = metric.value;

            row.appendChild(labelCell);
            row.appendChild(valueCell);
            tbody.appendChild(row);
        });

        table.appendChild(tbody);
        container.appendChild(table);
    }

    // Load config page
    function loadConfig(container) {
        container.textContent = '';

        const card = document.createElement('div');
        card.className = 'card';

        const header = document.createElement('div');
        header.style.cssText = 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px;';

        const title = document.createElement('h3');
        title.textContent = 'Configuration Editor';
        header.appendChild(title);

        const actions = document.createElement('div');
        actions.style.cssText = 'display: flex; gap: 8px;';

        const saveBtn = document.createElement('button');
        saveBtn.className = 'btn btn-primary';
        saveBtn.textContent = 'Save Config';
        saveBtn.id = 'save-config-btn';
        saveBtn.onclick = () => saveConfig();
        actions.appendChild(saveBtn);

        const validateBtn = document.createElement('button');
        validateBtn.className = 'btn btn-secondary';
        validateBtn.textContent = 'Validate';
        validateBtn.onclick = () => validateConfig();
        actions.appendChild(validateBtn);

        header.appendChild(actions);
        card.appendChild(header);

        const editorWrapper = document.createElement('div');
        editorWrapper.style.cssText = 'position: relative;';

        const editor = document.createElement('textarea');
        editor.id = 'config-editor';
        editor.style.cssText = 'width: 100%; min-height: 500px; font-family: "Cascadia Code", "Fira Code", monospace; font-size: 13px; line-height: 1.5; padding: 16px; border: 1px solid var(--border); border-radius: 8px; background: var(--bg-tertiary); color: var(--text-primary); resize: vertical; tab-size: 2;';
        editor.spellcheck = false;
        editor.placeholder = 'Loading configuration...';
        editorWrapper.appendChild(editor);

        card.appendChild(editorWrapper);

        // Status bar
        const statusBar = document.createElement('div');
        statusBar.id = 'config-status';
        statusBar.style.cssText = 'margin-top: 12px; padding: 8px 12px; border-radius: 6px; font-size: 13px; display: none;';
        card.appendChild(statusBar);

        container.appendChild(card);
        fetchConfigFile();
    }

    // Fetch config file from API
    async function fetchConfigFile() {
        try {
            const response = await fetch('/api/v1/config/file');
            if (!response.ok) throw new Error('Failed to fetch config file');
            const text = await response.text();
            const editor = document.getElementById('config-editor');
            if (editor) editor.value = text;
        } catch (err) {
            const editor = document.getElementById('config-editor');
            if (editor) {
                editor.value = '# Error loading config: ' + err.message + '\n# Falling back to JSON view...\n';
            }
            // Fallback to JSON config
            fetchConfigFallback();
        }
    }

    // Fallback: fetch JSON config if YAML endpoint fails
    async function fetchConfigFallback() {
        try {
            const response = await fetch('/api/v1/config');
            if (!response.ok) throw new Error('Failed to fetch config');
            const data = await response.json();
            const editor = document.getElementById('config-editor');
            if (editor) editor.value += '\n' + JSON.stringify(data, null, 2);
        } catch (err) {
            console.error('Config fallback also failed:', err);
        }
    }

    // Save config file
    async function saveConfig() {
        const editor = document.getElementById('config-editor');
        const saveBtn = document.getElementById('save-config-btn');
        if (!editor || !saveBtn) return;

        saveBtn.disabled = true;
        saveBtn.textContent = 'Saving...';

        try {
            const response = await fetch('/api/v1/config/file', {
                method: 'PUT',
                headers: { 'Content-Type': 'text/yaml', 'X-Requested-With': 'XMLHttpRequest' },
                body: editor.value
            });

            const data = await response.json().catch(() => ({}));
            if (!response.ok) throw new Error(data.error || 'Save failed');

            showConfigStatus('Configuration saved successfully. Click "Reload Config" to apply changes.', 'success');
        } catch (err) {
            showConfigStatus('Failed to save: ' + err.message, 'error');
        } finally {
            saveBtn.disabled = false;
            saveBtn.textContent = 'Save Config';
        }
    }

    // Validate config without saving
    async function validateConfig() {
        const editor = document.getElementById('config-editor');
        if (!editor) return;

        try {
            const response = await fetch('/api/v1/config/validate', {
                method: 'POST',
                headers: { 'Content-Type': 'text/yaml', 'X-Requested-With': 'XMLHttpRequest' },
                body: editor.value
            });

            const data = await response.json().catch(() => ({}));
            if (!response.ok) {
                showConfigStatus('Validation failed: ' + (data.error || 'Unknown error'), 'error');
                return;
            }
            showConfigStatus('Configuration is valid', 'success');
        } catch (err) {
            showConfigStatus('Validation failed: ' + err.message, 'error');
        }
    }

    // Show config status message
    function showConfigStatus(message, type) {
        const statusBar = document.getElementById('config-status');
        if (!statusBar) return;
        statusBar.style.display = 'block';
        statusBar.style.background = type === 'success' ? 'rgba(52, 211, 153, 0.1)' : 'rgba(239, 68, 68, 0.1)';
        statusBar.style.color = type === 'success' ? 'var(--success)' : 'var(--error)';
        statusBar.style.border = '1px solid ' + (type === 'success' ? 'var(--success)' : 'var(--error)');
        statusBar.textContent = message;
        setTimeout(() => { statusBar.style.display = 'none'; }, 5000);
    }

    // Fetch configuration (legacy)
    async function fetchConfig() {
        try {
            const response = await fetch('/api/v1/config');
            if (!response.ok) throw new Error('Failed to fetch config');
            const data = await response.json();
            updateConfigContent(data);
        } catch (err) {
            const content = document.getElementById('config-content');
            if (content) content.textContent = 'Error loading config: ' + err.message;
        }
    }

    // Update config content
    function updateConfigContent(data) {
        const container = document.getElementById('config-content');
        if (!container) return;
        container.textContent = '';

        const pre = document.createElement('pre');
        pre.style.cssText = 'background: var(--bg-tertiary); padding: 16px; border-radius: 8px; overflow: auto;';
        pre.textContent = JSON.stringify(data, null, 2);
        container.appendChild(pre);
    }

    // Reload configuration
    async function reloadConfig() {
        const btn = elements.reloadConfigBtn;
        if (btn) {
            btn.disabled = true;
            btn.textContent = 'Reloading...';
        }

        try {
            const response = await fetch('/api/v1/config/reload', { method: 'POST', headers: { 'X-Requested-With': 'XMLHttpRequest' } });
            if (!response.ok) throw new Error('Reload failed');
            const data = await response.json();
            showNotification(data.message || 'Configuration reloaded successfully', 'success');
            refreshCurrentPage();
        } catch (err) {
            showNotification('Failed to reload config: ' + err.message, 'error');
        } finally {
            if (btn) {
                btn.disabled = false;
                btn.textContent = 'Reload Config';
            }
        }
    }

    // Drain backend
    async function drainBackend(address) {
        try {
            const response = await fetch('/api/v1/backends/' + encodeURIComponent(address) + '/drain', {
                method: 'POST',
                headers: { 'X-Requested-With': 'XMLHttpRequest' }
            });
            if (!response.ok) throw new Error('Drain failed');
            showNotification('Backend ' + address + ' draining', 'success');
            refreshCurrentPage();
        } catch (err) {
            showNotification('Failed to drain backend: ' + err.message, 'error');
        }
    }

    // Show notification
    function showNotification(message, type) {
        const notif = document.createElement('div');
        notif.style.cssText = 'position: fixed; top: 24px; right: 24px; padding: 16px 24px; ' +
            'border-radius: 8px; background: ' + (type === 'success' ? 'var(--success)' : 'var(--error)') +
            '; color: white; z-index: 1000; animation: slideIn 0.3s ease;';
        notif.textContent = message;
        document.body.appendChild(notif);
        setTimeout(() => notif.remove(), 3000);
    }

    // Start realtime updates via SSE
    function startRealtimeUpdates() {
        if (state.eventSource) return;

        try {
            state.eventSource = new EventSource('/api/v1/stats/stream');
            state.eventSource.onmessage = (e) => {
                try {
                    const data = JSON.parse(e.data);
                    // Track QPS time-series
                    const qps = data.queries_per_sec || 0;
                    state.qpsHistory.push({ time: Date.now(), value: qps });
                    if (state.qpsHistory.length > state.qpsMaxPoints) {
                        state.qpsHistory.shift();
                    }
                    // Draw QPS chart if on overview page
                    if (state.currentPage === 'overview' && state.qpsHistory.length > 1) {
                        drawQPSChart();
                    }

                    // Update overview page stats
                    if (state.currentPage === 'overview') {
                        updateStatElement('stat-connections', data.total_connections);
                        updateStatElement('stat-pools', data.active_pools);
                        updateStatElement('stat-qps', data.queries_per_sec);
                        if (data.cache_hit_rate !== undefined) {
                            updateStatElement('stat-cache-hit', data.cache_hit_rate + '%');
                        }
                        updateStatElement('stat-active-tx', data.active_transactions);
                    }
                    // Update queries page stats
                    if (state.currentPage === 'queries') {
                        updateStatElement('stat-total-queries', data.total_queries);
                        updateStatElement('stat-cached-queries', data.cached_queries);
                        const total = data.total_queries || 0;
                        const cached = data.cached_queries || 0;
                        const hitRate = total > 0 ? ((cached / total) * 100).toFixed(1) + '%' : '0%';
                        updateStatElement('stat-cache-hit', hitRate);
                    }
                    // Update transactions page stats
                    if (state.currentPage === 'transactions') {
                        updateStatElement('stat-active-tx', data.active_transactions);
                    }
                } catch (err) {
                    console.error('Failed to parse SSE data:', err);
                }
            };
            state.eventSource.onerror = () => {
                updateConnectionStatus(false);
                state.eventSource.close();
                state.eventSource = null;
            };
            state.eventSource.onopen = () => {
                updateConnectionStatus(true);
            };
        } catch (err) {
            console.error('SSE not supported');
        }
    }

    // Stop realtime updates
    function stopRealtimeUpdates() {
        if (state.eventSource) {
            state.eventSource.close();
            state.eventSource = null;
        }
    }

    // Update connection status indicator
    function updateConnectionStatus(connected) {
        if (elements.connectionStatus) {
            elements.connectionStatus.textContent = connected ? 'Connected' : 'Disconnected';
        }
        if (elements.statusIndicator) {
            elements.statusIndicator.className = 'status-indicator ' + (connected ? 'online' : 'offline');
        }
    }

    // Load users page
    function loadUsers(container) {
        container.textContent = '';

        const card = document.createElement('div');
        card.className = 'card';

        const header = document.createElement('div');
        header.style.cssText = 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px;';

        const title = document.createElement('h3');
        title.textContent = 'Users';
        header.appendChild(title);

        const addBtn = document.createElement('button');
        addBtn.className = 'btn btn-primary';
        addBtn.textContent = 'Add User';
        addBtn.onclick = () => showAddUserModal();
        header.appendChild(addBtn);

        card.appendChild(header);

        const content = document.createElement('div');
        content.id = 'users-content';
        content.textContent = 'Loading users...';
        card.appendChild(content);

        container.appendChild(card);
        fetchUsers();
    }

    // Fetch users from API
    async function fetchUsers() {
        try {
            const response = await fetch('/api/v1/users');
            if (!response.ok) throw new Error('Failed to fetch users');
            const data = await response.json();
            updateUsersContent(data);
        } catch (err) {
            const content = document.getElementById('users-content');
            if (content) content.textContent = 'Error loading users: ' + err.message;
        }
    }

    // Update users content
    function updateUsersContent(data) {
        const container = document.getElementById('users-content');
        if (!container) return;
        container.textContent = '';

        if (!data.users || data.users.length === 0) {
            container.textContent = 'No users configured';
            return;
        }

        const table = document.createElement('table');
        table.className = 'data-table';

        const thead = document.createElement('thead');
        const headerRow = document.createElement('tr');
        ['Username', 'Max Connections', 'Default Pool', 'Allowed Pools', 'Actions'].forEach(text => {
            const th = document.createElement('th');
            th.textContent = text;
            headerRow.appendChild(th);
        });
        thead.appendChild(headerRow);
        table.appendChild(thead);

        const tbody = document.createElement('tbody');
        data.users.forEach(user => {
            const row = document.createElement('tr');

            const nameCell = document.createElement('td');
            nameCell.textContent = user.username || '-';

            const maxConnCell = document.createElement('td');
            maxConnCell.textContent = user.max_connections || 0;

            const defaultPoolCell = document.createElement('td');
            defaultPoolCell.textContent = user.default_pool || '-';

            const allowedPoolsCell = document.createElement('td');
            allowedPoolsCell.textContent = (user.allowed_pools && user.allowed_pools.length > 0) ? user.allowed_pools.join(', ') : '*';

            const actionsCell = document.createElement('td');
            const deleteBtn = document.createElement('button');
            deleteBtn.className = 'btn btn-secondary';
            deleteBtn.textContent = 'Delete';
            deleteBtn.addEventListener('click', () => deleteUser(user.username));
            actionsCell.appendChild(deleteBtn);

            row.appendChild(nameCell);
            row.appendChild(maxConnCell);
            row.appendChild(defaultPoolCell);
            row.appendChild(allowedPoolsCell);
            row.appendChild(actionsCell);
            tbody.appendChild(row);
        });
        table.appendChild(tbody);
        container.appendChild(table);
    }

    // Delete user
    async function deleteUser(username) {
        if (!confirm('Delete user "' + username + '"?')) return;
        try {
            const response = await fetch('/api/v1/users/' + encodeURIComponent(username), {
                method: 'DELETE',
                headers: { 'X-Requested-With': 'XMLHttpRequest' }
            });
            if (!response.ok) {
                const errData = await response.json().catch(() => ({}));
                throw new Error(errData.error || 'Delete failed');
            }
            showNotification('User "' + username + '" deleted', 'success');
            fetchUsers();
        } catch (err) {
            showNotification('Failed to delete user: ' + err.message, 'error');
        }
    }

    // Show add user modal using safe DOM methods
    function showAddUserModal() {
        // Create modal overlay
        const overlay = document.createElement('div');
        overlay.style.cssText = 'position: fixed; top: 0; left: 0; width: 100%; height: 100%; background: rgba(0,0,0,0.7); display: flex; align-items: center; justify-content: center; z-index: 1000;';

        // Create modal content
        const modal = document.createElement('div');
        modal.style.cssText = 'background: var(--bg-secondary); border-radius: 12px; padding: 24px; max-width: 500px; width: 90%;';

        // Header
        const header = document.createElement('div');
        header.style.cssText = 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px;';

        const modalTitle = document.createElement('h2');
        modalTitle.textContent = 'Add User';
        modalTitle.style.margin = '0';
        header.appendChild(modalTitle);

        const closeBtn = document.createElement('button');
        closeBtn.textContent = '\u2715';
        closeBtn.style.cssText = 'background: none; border: none; color: var(--text-primary); font-size: 20px; cursor: pointer;';
        closeBtn.onclick = () => overlay.remove();
        header.appendChild(closeBtn);
        modal.appendChild(header);

        // Form fields
        const fields = [
            { id: 'new-username', label: 'Username', type: 'text', placeholder: 'Enter username' },
            { id: 'new-password', label: 'Password', type: 'password', placeholder: 'Enter password' },
            { id: 'new-max-connections', label: 'Max Connections', type: 'number', placeholder: '0 = unlimited', value: '0' },
            { id: 'new-default-pool', label: 'Default Pool', type: 'text', placeholder: 'Optional' },
            { id: 'new-allowed-pools', label: 'Allowed Pools (comma-separated)', type: 'text', placeholder: 'Empty = all pools' }
        ];

        fields.forEach(field => {
            const group = document.createElement('div');
            group.style.cssText = 'margin-bottom: 16px;';

            const label = document.createElement('label');
            label.textContent = field.label;
            label.style.cssText = 'display: block; margin-bottom: 4px; color: var(--text-secondary); font-size: 14px;';
            group.appendChild(label);

            const input = document.createElement('input');
            input.type = field.type;
            input.id = field.id;
            input.className = 'form-input';
            input.placeholder = field.placeholder || '';
            if (field.value !== undefined) input.value = field.value;
            input.style.cssText = 'width: 100%; padding: 8px 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--bg-tertiary); color: var(--text-primary);';
            group.appendChild(input);

            modal.appendChild(group);
        });

        // Submit button
        const submitBtn = document.createElement('button');
        submitBtn.className = 'btn btn-primary';
        submitBtn.textContent = 'Create User';
        submitBtn.style.cssText = 'width: 100%; margin-top: 8px;';
        submitBtn.onclick = () => createUser(overlay);
        modal.appendChild(submitBtn);

        overlay.appendChild(modal);
        document.body.appendChild(overlay);

        // Close on overlay click
        overlay.onclick = (e) => {
            if (e.target === overlay) overlay.remove();
        };
    }

    // Create user from modal form
    async function createUser(overlay) {
        const username = document.getElementById('new-username').value.trim();
        const password = document.getElementById('new-password').value.trim();
        const maxConnections = parseInt(document.getElementById('new-max-connections').value) || 0;
        const defaultPool = document.getElementById('new-default-pool').value.trim() || null;
        const allowedPoolsStr = document.getElementById('new-allowed-pools').value.trim();

        if (!username) {
            showNotification('Username is required', 'error');
            return;
        }
        if (!password) {
            showNotification('Password is required', 'error');
            return;
        }

        const body = {
            username: username,
            password_hash: password,
            max_connections: maxConnections
        };
        if (defaultPool) body.default_pool = defaultPool;
        if (allowedPoolsStr) body.allowed_pools = allowedPoolsStr.split(',').map(s => s.trim()).filter(s => s);

        try {
            const response = await fetch('/api/v1/users', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
                body: JSON.stringify(body)
            });
            if (!response.ok) {
                const errData = await response.json().catch(() => ({}));
                throw new Error(errData.error || 'Create failed');
            }
            showNotification('User "' + username + '" created', 'success');
            overlay.remove();
            fetchUsers();
        } catch (err) {
            showNotification('Failed to create user: ' + err.message, 'error');
        }
    }

    // Capitalize string
    function capitalize(str) {
        return str.charAt(0).toUpperCase() + str.slice(1);
    }

    // Initialize when DOM is ready
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();

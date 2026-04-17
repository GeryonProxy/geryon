// Geryon Dashboard JavaScript
(function() {
    'use strict';

    // Dashboard state
    const state = {
        currentPage: 'overview',
        stats: null,
        eventSource: null,
        refreshInterval: null
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
            case 'transactions':
                loadTransactions(content);
                break;
            case 'config':
                loadConfig(content);
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

        const title = document.createElement('h3');
        title.textContent = 'Backend Servers';
        card.appendChild(title);

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
            const response = await fetch('/api/v1/health');
            if (!response.ok) throw new Error('Failed to fetch backends');
            const data = await response.json();
            updateBackendsContent(data);
        } catch (err) {
            const content = document.getElementById('backends-content');
            if (content) content.textContent = 'Error loading backends: ' + err.message;
        }
    }

    // Update backends content
    function updateBackendsContent(data) {
        const container = document.getElementById('backends-content');
        if (!container) return;
        container.textContent = '';

        if (!data.backends || data.backends.length === 0) {
            container.textContent = 'No backends configured';
            return;
        }

        const table = document.createElement('table');
        table.className = 'data-table';

        const thead = document.createElement('thead');
        const headerRow = document.createElement('tr');
        ['Address', 'Status', 'Latency', 'Last Check', 'Actions'].forEach(text => {
            const th = document.createElement('th');
            th.textContent = text;
            headerRow.appendChild(th);
        });
        thead.appendChild(headerRow);
        table.appendChild(thead);

        const tbody = document.createElement('tbody');
        data.backends.forEach(backend => {
            const row = document.createElement('tr');

            const addressCell = document.createElement('td');
            addressCell.textContent = backend.address || '-';

            const statusCell = document.createElement('td');
            const status = document.createElement('span');
            status.className = 'status ' + (backend.status || 'unknown');
            status.textContent = backend.status || 'unknown';
            statusCell.appendChild(status);

            const latencyCell = document.createElement('td');
            latencyCell.textContent = backend.latency || 'N/A';

            const lastCheckCell = document.createElement('td');
            lastCheckCell.textContent = backend.last_check || 'N/A';

            const actionsCell = document.createElement('td');
            const drainBtn = document.createElement('button');
            drainBtn.className = 'btn btn-secondary';
            drainBtn.textContent = 'Drain';
            drainBtn.addEventListener('click', () => drainBackend(backend.address));
            actionsCell.appendChild(drainBtn);

            row.appendChild(addressCell);
            row.appendChild(statusCell);
            row.appendChild(latencyCell);
            row.appendChild(lastCheckCell);
            row.appendChild(actionsCell);
            tbody.appendChild(row);
        });
        table.appendChild(tbody);
        container.appendChild(table);
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

        const title = document.createElement('h3');
        title.textContent = 'Configuration';
        card.appendChild(title);

        const content = document.createElement('div');
        content.id = 'config-content';
        content.textContent = 'Loading configuration...';
        card.appendChild(content);

        container.appendChild(card);
        fetchConfig();
    }

    // Fetch configuration
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
            const response = await fetch('/api/v1/config/reload', { method: 'POST' });
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
                method: 'POST'
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

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
            const [statsRes, healthRes, poolsRes] = await Promise.all([
                fetch('/api/v1/stats').catch(() => null),
                fetch('/api/v1/health').catch(() => null),
                fetch('/api/v1/pools').catch(() => null)
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

        // Placeholder - would fetch from API
        setTimeout(() => {
            content.textContent = 'Connection tracking coming soon';
        }, 500);
    }

    // Load queries page
    function loadQueries(container) {
        container.textContent = '';

        const card = document.createElement('div');
        card.className = 'card';

        const title = document.createElement('h3');
        title.textContent = 'Query Statistics';
        card.appendChild(title);

        const content = document.createElement('div');
        content.textContent = 'Query analytics coming soon';
        card.appendChild(content);

        container.appendChild(card);
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
            showNotification('Configuration reloaded successfully', 'success');
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
                    if (state.currentPage === 'overview') {
                        updateStatElement('stat-connections', data.total_connections);
                        updateStatElement('stat-qps', data.queries_per_sec);
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

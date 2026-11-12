// LionSQL Flat SPA JavaScript Application
document.addEventListener('DOMContentLoaded', () => {
  initRouter();
  loadTables();
  initQueryRunner();
});

function initRouter() {
  function handleRoute() {
    const hash = window.location.hash || '#/tables';
    const viewName = hash.replace('#/', '');

    document.querySelectorAll('.view-section').forEach(sec => sec.classList.remove('active'));
    document.querySelectorAll('.nav-link').forEach(link => link.classList.remove('active'));

    const targetSec = document.getElementById('view-' + viewName);
    const targetLink = document.querySelector(`.nav-link[data-view="${viewName}"]`);

    if (targetSec) {
      targetSec.classList.add('active');
    } else {
      document.getElementById('view-tables').classList.add('active');
    }

    if (targetLink) {
      targetLink.classList.add('active');
    }
  }

  window.addEventListener('hashchange', handleRoute);
  handleRoute();
}

async function loadTables() {
  const container = document.getElementById('tables-list');
  try {
    const res = await fetch('/api/query', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({query: "SELECT name, type_desc FROM sys.objects WHERE type IN ('U', 'V') ORDER BY name;"})
    });
    if (!res.ok) {
      container.innerHTML = '<div style="color: var(--color-error);">Failed to load tables.</div>';
      return;
    }
    const data = await res.json();
    const rows = data.rows || [];
    if (rows.length === 0) {
      container.innerHTML = '<div style="color: var(--text-muted); font-size: 0.875rem;">No user tables found in database.</div>';
      return;
    }

    let html = '<table class="data-table"><thead><tr><th>Object Name</th><th>Type</th><th>Actions</th></tr></thead><tbody>';
    for (const r of rows) {
      const name = r.name || r[0];
      const type = r.type_desc || r[1] || 'USER_TABLE';
      html += `<tr><td><strong>${escapeHtml(name)}</strong></td><td>${escapeHtml(type)}</td><td><button class="btn-primary" style="padding: 2px 8px; font-size: 0.75rem;" onclick="queryTable('${escapeHtml(name)}')">View Data</button></td></tr>`;
    }
    html += '</tbody></table>';
    container.innerHTML = html;
  } catch (err) {
    container.innerHTML = `<div style="color: var(--color-error); font-size: 0.875rem;">Error: ${escapeHtml(err.message)}</div>`;
  }
}

window.queryTable = function(tableName) {
  window.location.hash = '#/query';
  const input = document.getElementById('spa-query-input');
  input.value = `SELECT TOP (50) * FROM [${tableName}];`;
  document.getElementById('spa-run-btn').click();
};

function initQueryRunner() {
  const btn = document.getElementById('spa-run-btn');
  const input = document.getElementById('spa-query-input');
  const resultDiv = document.getElementById('spa-query-result');

  btn.addEventListener('click', async () => {
    const sql = input.value.trim();
    if (!sql) return;

    btn.disabled = true;
    btn.textContent = 'Executing...';
    resultDiv.innerHTML = '<div style="color: var(--text-muted); font-size: 0.875rem;">Running query...</div>';

    try {
      const res = await fetch('/api/query', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({query: sql})
      });
      const data = await res.json();
      if (!res.ok || data.error) {
        resultDiv.innerHTML = `<div style="color: var(--color-error); font-size: 0.875rem; font-family: monospace;">${escapeHtml(data.error || 'Execution failed')}</div>`;
        return;
      }

      const rows = data.rows || [];
      const cols = data.columns || (rows.length > 0 ? Object.keys(rows[0]) : []);

      if (rows.length === 0) {
        resultDiv.innerHTML = '<div style="color: var(--color-success); font-size: 0.875rem;">Command completed successfully (0 rows).</div>';
        return;
      }

      let html = '<table class="data-table"><thead><tr>';
      for (const c of cols) {
        html += `<th>${escapeHtml(c)}</th>`;
      }
      html += '</tr></thead><tbody>';

      for (const r of rows) {
        html += '<tr>';
        for (const c of cols) {
          const val = Array.isArray(r) ? r[cols.indexOf(c)] : r[c];
          html += `<td>${val !== null && val !== undefined ? escapeHtml(String(val)) : '<span style="color: #64748b;">NULL</span>'}</td>`;
        }
        html += '</tr>';
      }
      html += '</tbody></table>';
      resultDiv.innerHTML = html;
    } catch (e) {
      resultDiv.innerHTML = `<div style="color: var(--color-error); font-size: 0.875rem;">Network Error: ${escapeHtml(e.message)}</div>`;
    } finally {
      btn.disabled = false;
      btn.textContent = 'Execute';
    }
  });
}

function escapeHtml(str) {
  return String(str).replace(/[&<>'"]/g, tag => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    "'": '&#39;',
    '"': '&quot;'
  }[tag] || tag));
}

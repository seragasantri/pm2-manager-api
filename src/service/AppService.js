const Docker = require('dockerode');
const { execFile } = require('child_process');
const App = require('../model/App');

// Connect ke Docker daemon via Unix socket
const docker = new Docker({ socketPath: '/var/run/docker.sock' });

// Environment untuk PM2 CLI — connect ke daemon host
const PM2_ENV = { ...process.env, PM2_HOME: '/root/.pm2' };

class AppService {
  /**
   * Execute PM2 CLI via execFile (aman dari command injection)
   * @param {string[]} args - array argumen, misal ['jlist'] atau ['restart', 'myapp']
   */
  static execPM2(args) {
    return new Promise((resolve, reject) => {
      execFile('pm2', args, { env: PM2_ENV }, (error, stdout, stderr) => {
        if (error) {
          reject(new Error(stderr || error.message));
        } else {
          try {
            resolve(JSON.parse(stdout));
          } catch (e) {
            resolve(stdout);
          }
        }
      });
    });
  }

  /**
   * Ambil daftar semua apps (Docker containers + PM2 processes)
   */
  static async getApps(user) {
    const apps = [];

    // 1. Ambil Docker containers
    try {
      const containers = await docker.listContainers({ all: true });
      const dockerApps = containers.map(c => {
        const name = (c.Names?.[0] || '').replace(/^\//, '');
        const state = c.State;

        let status = 'unknown';
        if (state === 'running') status = 'online';
        else if (state === 'exited') status = 'stopped';
        else if (state === 'paused') status = 'stopped';
        else if (state === 'restarting') status = 'processing';
        else status = state;

        return {
          name,
          status,
          cpu: 0,
          memory: 0,
          uptime: c.Created ? c.Created * 1000 : null,
          pm_id: c.Id?.substring(0, 12) || '',
          cwd: '',
          containerId: c.Id,
          image: c.Image,
          type: 'docker',
          ports: c.Ports || [],
        };
      });
      apps.push(...dockerApps);
    } catch (err) {
      console.error('Gagal ambil Docker containers:', err.message);
    }

    // 2. Ambil PM2 processes via CLI
    try {
      const pm2List = await this.execPM2(['jlist']);

      const pm2Apps = pm2List.map(app => {
        const name = app.name;
        // Skip jika sudah ada di Docker (hindari duplikasi)
        if (apps.find(a => a.name === name)) return null;

        return {
          name,
          status: app.pm2_env?.status || 'unknown',
          cpu: app.monit?.cpu || 0,
          memory: app.monit?.memory || 0,
          uptime: app.pm2_env?.pm_uptime,
          pm_id: app.pm2_env?.pm_id || 0,
          cwd: app.pm2_env?.pm_cwd || '',
          type: 'pm2',
        };
      }).filter(Boolean);

      apps.push(...pm2Apps);
    } catch (err) {
      console.error('Gagal ambil PM2 processes:', err.message);
    }

    // Sync apps ke database
    await App.syncFromPM2(apps);

    // Filter by allowed apps jika bukan superadmin
    if (user.role !== 'superadmin' && user.allowedApps) {
      return apps.filter(app => user.allowedApps.includes(app.name));
    }

    return apps;
  }

  /**
   * Lakukan aksi start/stop/restart pada app (Docker atau PM2)
   */
  static async doAction(appName, action, user) {
    // Check permission untuk non-superadmin
    if (user.role !== 'superadmin' && user.allowedApps) {
      if (!user.allowedApps.includes(appName)) {
        throw new Error('Anda tidak memiliki akses ke aplikasi ini');
      }
    }

    // Cari app di Docker dulu
    const containers = await docker.listContainers({ all: true });
    const container = containers.find(c => {
      const name = (c.Names?.[0] || '').replace(/^\//, '');
      return name === appName;
    });

    if (container) {
      // Handle Docker container
      const cont = docker.getContainer(container.Id);

      switch (action) {
        case 'start':
          await cont.start();
          break;
        case 'stop':
          await cont.stop();
          break;
        case 'restart':
          await cont.restart();
          break;
        default:
          throw new Error(`Aksi '${action}' tidak didukung. Gunakan start, stop, atau restart.`);
      }

      return { action, container: appName, type: 'docker' };
    }

    // Jika tidak ada di Docker, coba PM2 via CLI
    try {
      await this.execPM2([action, appName]);
      return { action, container: appName, type: 'pm2' };
    } catch (err) {
      throw new Error(`Gagal melakukan aksi '${action}' pada '${appName}': ${err.message}`);
    }
  }

  /**
   * Ambil stats real-time (CPU & Memory) untuk satu container
   */
  static async getContainerStats(containerName) {
    const containers = await docker.listContainers({ all: false });
    const container = containers.find(c => {
      const name = (c.Names?.[0] || '').replace(/^\//, '');
      return name === containerName;
    });

    if (!container) return null;

    const cont = docker.getContainer(container.Id);
    const stats = await cont.stats({ stream: false });

    // Hitung CPU usage
    const cpuDelta = stats.cpu_stats.cpu_usage.total_usage - stats.precpu_stats.cpu_usage.total_usage;
    const systemDelta = stats.cpu_stats.system_cpu_usage - stats.precpu_stats.system_cpu_usage;
    const numCpus = stats.cpu_stats.online_cpus || 1;
    const cpuPercent = systemDelta > 0 ? (cpuDelta / systemDelta) * numCpus * 100 : 0;

    // Hitung Memory usage
    const memUsed = stats.memory_stats.usage - (stats.memory_stats.stats?.cache || 0);
    const memLimit = stats.memory_stats.limit;

    return {
      cpu: Math.round(cpuPercent * 100) / 100,
      memory: memUsed,
      memoryPercent: memLimit > 0 ? Math.round((memUsed / memLimit) * 10000) / 100 : 0,
    };
  }
}

module.exports = AppService;
const Docker = require('dockerode');
const App = require('../model/App');

// Connect ke Docker daemon via Unix socket
const docker = new Docker({ socketPath: '/var/run/docker.sock' });

class AppService {
  /**
   * Ambil daftar semua containers (running + stopped)
   */
  static async getApps(user) {
    const containers = await docker.listContainers({ all: true });

    const apps = containers.map(c => {
      const name = (c.Names?.[0] || '').replace(/^\//, '');
      const state = c.State; // running, exited, paused, dll

      // Map Docker state ke format yang sama seperti PM2 sebelumnya
      let status = 'unknown';
      if (state === 'running') status = 'online';
      else if (state === 'exited') status = 'stopped';
      else if (state === 'paused') status = 'stopped';
      else if (state === 'restarting') status = 'processing';
      else status = state;

      return {
        name,
        status,
        cpu: 0, // Docker API tidak menyediakan CPU real-time di list
        memory: 0, // Sama, perlu stats() terpisah
        uptime: c.Created ? c.Created * 1000 : null,
        pm_id: c.Id?.substring(0, 12) || '',
        cwd: '',
        containerId: c.Id,
        image: c.Image,
        ports: c.Ports || [],
      };
    });

    // Sync apps ke database
    await App.syncFromPM2(apps);

    // Filter by allowed apps jika bukan superadmin
    if (user.role !== 'superadmin' && user.allowedApps) {
      return apps.filter(app => user.allowedApps.includes(app.name));
    }

    return apps;
  }

  /**
   * Lakukan aksi start/stop/restart pada container
   */
  static async doAction(appName, action, user) {
    // Check permission untuk non-superadmin
    if (user.role !== 'superadmin' && user.allowedApps) {
      if (!user.allowedApps.includes(appName)) {
        throw new Error('Anda tidak memiliki akses ke aplikasi ini');
      }
    }

    // Cari container berdasarkan nama
    const containers = await docker.listContainers({ all: true });
    const container = containers.find(c => {
      const name = (c.Names?.[0] || '').replace(/^\//, '');
      return name === appName;
    });

    if (!container) {
      throw new Error(`Container '${appName}' tidak ditemukan`);
    }

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

    return { action, container: appName };
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
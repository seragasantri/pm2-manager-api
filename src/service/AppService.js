const pm2 = require('pm2');
const App = require('../model/App');

class AppService {
  static connectPM2() {
    return new Promise((resolve, reject) => {
      pm2.connect((err) => {
        if (err) reject(err);
        else resolve();
      });
    });
  }

  static disconnectPM2() {
    pm2.disconnect();
  }

  static async getApps(user) {
    await this.connectPM2();

    try {
      const list = await new Promise((resolve, reject) => {
        pm2.list((err, list) => {
          if (err) reject(err);
          else resolve(list);
        });
      });

      const apps = list.map(app => ({
        name: app.name,
        status: app.pm2_env?.status || 'unknown',
        cpu: app.monit?.cpu || 0,
        memory: app.monit?.memory || 0,
        uptime: app.pm2_env?.pm_uptime,
        pm_id: app.pm2_env?.pm_id,
        cwd: app.pm2_env?.pm_cwd,
      }));

      // Sync apps to database
      await App.syncFromPM2(apps);

      // Filter by allowed apps if not superadmin
      if (user.role !== 'superadmin' && user.allowedApps) {
        return apps.filter(app => user.allowedApps.includes(app.name));
      }

      return apps;
    } finally {
      this.disconnectPM2();
    }
  }

  static async doAction(appName, action, user) {
    // Check permission for non-superadmin
    if (user.role !== 'superadmin' && user.allowedApps) {
      if (!user.allowedApps.includes(appName)) {
        throw new Error('Anda tidak memiliki akses ke aplikasi ini');
      }
    }

    await this.connectPM2();

    try {
      return await new Promise((resolve, reject) => {
        pm2[action](appName, (err, result) => {
          if (err) reject(err);
          else resolve(result);
        });
      });
    } finally {
      this.disconnectPM2();
    }
  }
}

module.exports = AppService;
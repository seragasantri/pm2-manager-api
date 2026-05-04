const fs = require('fs-extra');
const path = require('path');
const pm2 = require('pm2');

class FileRepository {
  static getAppCwd(appName) {
    return new Promise((resolve, reject) => {
      pm2.describe(appName, (err, desc) => {
        if (err || !desc || desc.length === 0) {
          return reject(new Error('Aplikasi tidak ditemukan di PM2'));
        }
        resolve({
          cwd: desc[0].pm2_env.pm_cwd,
          pm_id: desc[0].pm2_env.pm_id,
        });
      });
    });
  }

  static async listFiles(appName, dir = '') {
    const { cwd } = await this.getAppCwd(appName);
    const targetPath = path.join(cwd, dir);

    // Security: Prevent path traversal
    if (!targetPath.startsWith(cwd)) {
      throw new Error('Akses direktori ditolak');
    }

    const items = await fs.readdir(targetPath, { withFileTypes: true });
    return {
      cwd,
      dir,
      files: items.map(item => ({
        name: item.name,
        isDirectory: item.isDirectory(),
        path: path.join(dir, item.name).replace(/\\/g, '/'),
      })).sort((a, b) => {
        if (a.isDirectory && !b.isDirectory) return -1;
        if (!a.isDirectory && b.isDirectory) return 1;
        return a.name.localeCompare(b.name);
      }),
    };
  }

  static async readFile(appName, filePath) {
    const { cwd } = await this.getAppCwd(appName);
    const targetPath = path.join(cwd, filePath);

    if (!targetPath.startsWith(cwd)) {
      throw new Error('Path tidak valid');
    }

    const content = await fs.readFile(targetPath, 'utf8');
    return { content };
  }

  static async writeFile(appName, filePath, content) {
    const { cwd } = await this.getAppCwd(appName);
    const targetPath = path.join(cwd, filePath);

    if (!targetPath.startsWith(cwd)) {
      throw new Error('Path tidak valid');
    }

    await fs.writeFile(targetPath, content, 'utf8');
    return { message: 'File tersimpan' };
  }

  static async deleteFile(appName, filePath) {
    const { cwd } = await this.getAppCwd(appName);
    const targetPath = path.join(cwd, filePath);

    if (!targetPath.startsWith(cwd)) {
      throw new Error('Path tidak valid');
    }

    await fs.remove(targetPath);
    return { message: 'Berhasil dihapus' };
  }

  static async createDir(appName, dirPath) {
    const { cwd } = await this.getAppCwd(appName);
    const targetPath = path.join(cwd, dirPath);

    if (!targetPath.startsWith(cwd)) {
      throw new Error('Path tidak valid');
    }

    await fs.ensureDir(targetPath);
    return { message: 'Folder dibuat' };
  }
}

module.exports = FileRepository;
const FileRepository = require('../repository/FileRepository');
const AppResponse = require('../http/response/AppResponse');

class FileController {
  static async checkAccess(appName, user) {
    if (user.role === 'superadmin') return true;
    if (!user.allowedApps || !user.allowedApps.includes(appName)) {
      return false;
    }
    return true;
  }

  static async index(req, res) {
    try {
      const { appName, dir = '' } = req.query;

      if (!appName) {
        return AppResponse.validationError(res, { appName: 'appName diperlukan' });
      }

      if (!FileController.checkAccess(appName, req.user)) {
        return AppResponse.forbidden(res, 'Anda tidak memiliki akses ke aplikasi ini');
      }

      const files = await FileRepository.listFiles(appName, dir);
      return AppResponse.success(res, files);
    } catch (error) {
      return AppResponse.error(res, error.message);
    }
  }

  static async read(req, res) {
    try {
      const { appName, filePath } = req.body;

      if (!appName || !filePath) {
        return AppResponse.validationError(res, { appName: 'appName diperlukan', filePath: 'filePath diperlukan' });
      }

      if (!FileController.checkAccess(appName, req.user)) {
        return AppResponse.forbidden(res, 'Anda tidak memiliki akses ke aplikasi ini');
      }

      const file = await FileRepository.readFile(appName, filePath);
      return AppResponse.success(res, file);
    } catch (error) {
      return AppResponse.error(res, error.message);
    }
  }

  static async write(req, res) {
    try {
      const { appName, filePath, content = '' } = req.body;

      if (!appName || !filePath) {
        return AppResponse.validationError(res, { appName: 'appName diperlukan', filePath: 'filePath diperlukan' });
      }

      if (!FileController.checkAccess(appName, req.user)) {
        return AppResponse.forbidden(res, 'Anda tidak memiliki akses ke aplikasi ini');
      }

      const result = await FileRepository.writeFile(appName, filePath, content);
      return AppResponse.success(res, result, 'File berhasil disimpan');
    } catch (error) {
      return AppResponse.error(res, error.message);
    }
  }

  static async delete(req, res) {
    try {
      const { appName, filePath } = req.body;

      if (!appName || !filePath) {
        return AppResponse.validationError(res, { appName: 'appName diperlukan', filePath: 'filePath diperlukan' });
      }

      if (!FileController.checkAccess(appName, req.user)) {
        return AppResponse.forbidden(res, 'Anda tidak memiliki akses ke aplikasi ini');
      }

      const result = await FileRepository.deleteFile(appName, filePath);
      return AppResponse.success(res, result, 'Berhasil dihapus');
    } catch (error) {
      return AppResponse.error(res, error.message);
    }
  }

  static async createDir(req, res) {
    try {
      const { appName, dirPath } = req.body;

      if (!appName || !dirPath) {
        return AppResponse.validationError(res, { appName: 'appName diperlukan', dirPath: 'dirPath diperlukan' });
      }

      if (!FileController.checkAccess(appName, req.user)) {
        return AppResponse.forbidden(res, 'Anda tidak memiliki akses ke aplikasi ini');
      }

      const result = await FileRepository.createDir(appName, dirPath);
      return AppResponse.success(res, result, 'Folder berhasil dibuat');
    } catch (error) {
      return AppResponse.error(res, error.message);
    }
  }
}

module.exports = FileController;
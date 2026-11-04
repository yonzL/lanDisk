<template>
  <el-result icon="warning" title="页面不存在" sub-title="检查地址是否正确，或返回首页。">
    <template #extra>
      <el-button type="primary" @click="$router.push('/')">回到首页</el-button>
    </template>
  </el-result>
</template>

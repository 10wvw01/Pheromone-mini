#ifndef PHEROMONE_MINI_V06_TASK_SCHEDULER_H
#define PHEROMONE_MINI_V06_TASK_SCHEDULER_H

#include <stddef.h>
#include <stdint.h>

typedef struct pm_task_context pm_task_context_t;

typedef enum {
    PM_INPUT_VALUE = 0,
    PM_INPUT_KEY_VALUE = 1,
    PM_INPUT_KEY_ONLY = 2
} pm_input_mode_t;

typedef int (*pm_object_send_fn)(
    const pm_task_context_t *task_ctx,
    const char *bucket,
    const char *key,
    const void *data,
    size_t size);

struct pm_task_context {
    const char *source_function;
    const char *session_id;
    pm_input_mode_t input_mode;
    const char *key;
    const void *value;
    size_t value_size;
    pm_object_send_fn send_object;
    void *object_ctx;
};

/* Every SO exports exactly this stateless entry point. */
int handler(const pm_task_context_t *ctx);

#endif
